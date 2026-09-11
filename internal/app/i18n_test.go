package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

func i18nTestApp(t *testing.T) *App {
	t.Helper()
	return &App{manager: &CoreManager{dataDir: t.TempDir(), state: defaultState()}, session: "test-session"}
}

func i18nSetLanguage(app *App, language string) {
	app.manager.mu.Lock()
	app.manager.state.Settings.Language = language
	app.manager.mu.Unlock()
}

// 服务端的提示是直接 toast apiResponse.Message 的，英文界面上露一句中文就很显眼。
// 这组用例盯的是 translateMessage 的三条路径：整句、带参数的正则，以及
// fmt.Errorf("...: %w") 包出来的多层文案。
func TestTranslateMessageCoversEachLayer(t *testing.T) {
	cases := []struct {
		name     string
		language string
		in       string
		want     string
	}{
		{"zh keeps the original", "zh-CN", "入口已保存", "入口已保存"},
		{"exact table", "en", "入口已保存", "Inbound saved"},
		{"pattern keeps the value", "en", "端口 443 已被入口 vless-in 使用", "Port 443 is already used by inbound vless-in"},
		{"wrapped error", "en", "入口 my node: 端口必须在 1-65535 之间", "Inbound my node: The port must be between 1 and 65535"},
		{"unknown text is left alone", "en", "这条提示还没进表", "这条提示还没进表"},
		{"partial hit keeps the unknown half", "en", "入口已保存: 这条提示还没进表", "Inbound saved: 这条提示还没进表"},
		{"ascii is never touched", "en", "method not allowed", "method not allowed"},
		// 这条提示自己就带 ": "，必须先整句匹配，否则会被拆成认不出的碎片。
		{
			"whole-string pattern beats the split", "en",
			"SNI 只能填写域名，不能包含空格或 / @ : ? # & 等字符: realm.example.com:8443",
			"The SNI must be a bare domain name, with no spaces and none of / @ : ? # &: realm.example.com:8443",
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if got := translateMessage(test.in, test.language); got != test.want {
				t.Fatalf("translateMessage(%q, %q) = %q, want %q", test.in, test.language, got, test.want)
			}
		})
	}
}

// 表里留了中文就等于没翻。
func TestEnglishTableHasNoLeftoverChinese(t *testing.T) {
	for key, value := range messagesEN {
		if hasCJK(value) {
			t.Fatalf("messagesEN[%q] is still Chinese: %q", key, value)
		}
	}
	for _, pattern := range messagePatternsEN {
		if hasCJK(pattern.to) {
			t.Fatalf("messagePatternsEN[%q] is still Chinese: %q", pattern.from, pattern.to)
		}
	}
}

// 这个菜单项由 cross-subscriptions.js 动态插入，但仍从 i18n.js 的 cs.menu 取文案。
// 两张表若复制成同一句英文，切换机制会正常执行，却看起来像完全没有翻译。
func TestCrossPanelSubscriptionMenuHasBothLanguages(t *testing.T) {
	dictionary, err := os.ReadFile("web/i18n.js")
	if err != nil {
		t.Fatal(err)
	}
	source := string(dictionary)
	for _, entry := range []string{
		`'cs.menu': '导出全部订阅（跨面板）'`,
		`'cs.menu': 'Export All Subscriptions (Cross Panel)'`,
	} {
		if !strings.Contains(source, entry) {
			t.Fatalf("web/i18n.js is missing %s", entry)
		}
	}
}

// "入口 %s: %w" 这类提示只能靠排在最后的通用兜底规则翻，代价是任何以同样前缀开头
// 的整句都会被它吞掉半句。所以这种整句必须在 messagesEN 里有确切条目。
func TestCatchAllPatternsCannotSwallowWholeSentences(t *testing.T) {
	prefixes := []string{}
	catchAll := regexp.MustCompile(`^\^(\S+) \(\(\?:\[\^:\]\|:\[\^ \]\)\+\)\$$`)
	for _, pattern := range messagePatternsEN {
		if match := catchAll.FindStringSubmatch(pattern.from.String()); match != nil {
			prefixes = append(prefixes, match[1]+" ")
		}
	}
	if len(prefixes) == 0 {
		t.Fatal("the catch-all patterns are gone, this guard needs updating")
	}
	literal := regexp.MustCompile(`"(?:[^"\\\n]|\\.)*"`)
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") || name == "i18n.go" {
			continue
		}
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		for _, raw := range literal.FindAllString(string(data), -1) {
			text, err := strconv.Unquote(raw)
			if err != nil || !hasCJK(text) {
				continue
			}
			for _, prefix := range prefixes {
				// 参数紧跟前缀的（"入口 %s: %w"）本来就是兜底规则的目标形状。
				if !strings.HasPrefix(text, prefix) || strings.HasPrefix(text[len(prefix):], "%") {
					continue
				}
				if _, ok := messagesEN[text]; !ok {
					t.Fatalf("%s: %q needs an exact messagesEN entry, the %q catch-all would swallow it", name, text, prefix)
				}
			}
		}
	}
}

// 语言取自面板设置，所以同一个请求换个设置就该换语言；normalizeLanguage 还要把空值
// 和乱填的值收回默认值。
func TestAPIMessagesFollowThePanelLanguage(t *testing.T) {
	app := i18nTestApp(t)
	call := func() string {
		request := httptest.NewRequest(http.MethodPost, "/api/inbounds", strings.NewReader(`{"name":"bad","type":"vless","listen":"0.0.0.0","port":0}`))
		request.AddCookie(&http.Cookie{Name: "mui_session", Value: "test-session"})
		recorder := httptest.NewRecorder()
		app.handleInbounds(recorder, request)
		if recorder.Code == http.StatusOK {
			t.Fatalf("the invalid inbound must be rejected, got %d", recorder.Code)
		}
		var response apiResponse
		if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		return response.Message
	}
	if message := call(); !hasCJK(message) {
		t.Fatalf("the default language is zh-CN, got %q", message)
	}
	i18nSetLanguage(app, "en")
	if message := call(); message == "" || hasCJK(message) {
		t.Fatalf("the English panel must not show Chinese, got %q", message)
	}
	for _, raw := range []string{"", "  ", "fr", "zh-TW"} {
		if got := normalizeLanguage(raw); got != defaultLanguage {
			t.Fatalf("normalizeLanguage(%q) = %q, want %q", raw, got, defaultLanguage)
		}
	}
	if got := normalizeLanguage(" en "); got != "en" {
		t.Fatalf("normalizeLanguage must trim the stored value, got %q", got)
	}
}

// 面板首页和登录页一样是静态资源，语言只能靠 <html data-default-language> 注进去，
// 否则第一次访问、localStorage 还空着的浏览器只会看到默认的中文。
func TestIndexPageCarriesThePanelLanguage(t *testing.T) {
	page, err := os.ReadFile("web/index.html")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(page), `data-default-language="zh-CN"`) {
		t.Fatal("web/index.html must ship the zh-CN placeholder that serveLocalizedPage rewrites")
	}
	app := i18nTestApp(t)
	i18nSetLanguage(app, "en")
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.AddCookie(&http.Cookie{Name: "mui_session", Value: "test-session"})
	recorder := httptest.NewRecorder()
	app.handleIndex(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("index page: status = %d", recorder.Code)
	}
	body := recorder.Body.String()
	if !strings.Contains(body, `data-default-language="en"`) || strings.Contains(body, `data-default-language="zh-CN"`) {
		t.Fatal("the index page must carry the stored language, replacing the placeholder")
	}
}

// 同步任务的提示先落进任务状态，等接口读的时候才翻，这样切换语言后再看同一个任务
// 也是对的。
func TestSyncJobMessagesAreTranslatedOnRead(t *testing.T) {
	app := i18nTestApp(t)
	i18nSetLanguage(app, "en")
	job := &inboundSyncJob{ID: "job", Targets: []inboundSyncTarget{{
		ID:      "peer",
		Message: "目标面板已断开，同步已停止",
		Results: []inboundSyncResult{{ID: "in", Message: "已保存，重启目标 Mihomo 后生效"}},
	}}}
	translated := app.trSyncJob(job)
	if hasCJK(translated.Targets[0].Message) || hasCJK(translated.Targets[0].Results[0].Message) {
		t.Fatalf("the English panel must not show Chinese: %+v", translated.Targets[0])
	}
	if job.Targets[0].Message != "目标面板已断开，同步已停止" || job.Targets[0].Results[0].Message != "已保存，重启目标 Mihomo 后生效" {
		t.Fatal("the stored job must keep the original wording")
	}
}

// 从 open 处的 '{' 数到配对的 '}'，按引号状态数，免得字符串里的括号把边界算歪。
func i18nBraceBlock(t *testing.T, source string, open int, what string) string {
	t.Helper()
	if open < 0 || open >= len(source) || source[open] != '{' {
		t.Fatalf("%s: cannot find the block this guard reads", what)
	}
	depth, quote := 0, byte(0)
	for i := open; i < len(source); i++ {
		character := source[i]
		switch {
		case quote != 0:
			if character == '\\' {
				i++
			} else if character == quote {
				quote = 0
			}
		case character == '\'' || character == '"':
			quote = character
		case character == '{':
			depth++
		case character == '}':
			depth--
			if depth == 0 {
				return source[open : i+1]
			}
		}
	}
	t.Fatalf("%s: the block is never closed", what)
	return ""
}

var i18nSourceKey = regexp.MustCompile(`'((?:[^'\\]|\\.)*)'\s*:`)

// i18n.js 里 SOURCE_* 那几张表的键，也就是"这段原文认得出来"的全部清单。
func i18nTableKeys(t *testing.T, source, name string) map[string]bool {
	t.Helper()
	marker := strings.Index(source, "var "+name+" = {")
	if marker < 0 {
		t.Fatalf("web/i18n.js no longer defines %s, this guard needs updating", name)
	}
	block := i18nBraceBlock(t, source, marker+strings.Index(source[marker:], "{"), name)
	unescape := strings.NewReplacer(`\'`, `'`, `\\`, `\`)
	keys := map[string]bool{}
	for _, match := range i18nSourceKey.FindAllStringSubmatch(block, -1) {
		keys[unescape.Replace(match[1])] = true
	}
	if len(keys) == 0 {
		t.Fatalf("%s came out empty, this guard needs updating", name)
	}
	return keys
}

// 提示属性（placeholder / title / aria-label）和正文各走各的表，漏一条就在英文面板上
// 留一句中文；title 还更阴，只有悬停才露出来，扫一眼页面根本看不见。三件事：面板页
// 上写死的中文属性有没有出口、兜底那一遍能不能走到属性、自带译文的页面有没有把每个
// 中文属性都换掉。
func TestTranslatableAttributesAreReachable(t *testing.T) {
	dictionary, err := os.ReadFile("web/i18n.js")
	if err != nil {
		t.Fatal(err)
	}
	source := string(dictionary)
	// 兜底那一遍先查属性表、再按 SKIP_TAGS 收手。SKIP_TAGS 拦的是"别动这个标签的正文"
	// （textarea 里是用户输入、option 是协议关键字），提示属性照翻不误。顺序反过来，
	// <textarea> 上的 placeholder/title 就再也翻不到了。
	if !strings.Contains(source, "TEXTAREA: 1") {
		t.Fatal("TEXTAREA left SKIP_TAGS, this guard needs updating")
	}
	attributes, text := strings.Index(source, "SOURCE_PH[ph]"), strings.Index(source, "SKIP_TAGS[node2.tagName]")
	if attributes < 0 || text < 0 {
		t.Fatal("muiApply's second pass no longer looks like this guard expects")
	}
	if attributes > text {
		t.Fatal("the SKIP_TAGS bail-out must come after the placeholder lookup, or textarea hints stop being translated")
	}

	tables := map[string]map[string]bool{
		"placeholder": i18nTableKeys(t, source, "SOURCE_PH"),
		"title":       i18nTableKeys(t, source, "SOURCE_TITLE"),
		"aria-label":  i18nTableKeys(t, source, "SOURCE_ARIA"),
	}
	stamps := map[string]string{"placeholder": "data-i18n-placeholder", "title": "data-i18n-title", "aria-label": "data-i18n-aria"}
	tag := regexp.MustCompile(`<[a-zA-Z][^>]*>`)
	attribute := regexp.MustCompile(`(placeholder|title|aria-label)="([^"]*)"`)
	chineseAttributes := func(markup string) [][2]string {
		found := [][2]string{}
		for _, element := range tag.FindAllString(markup, -1) {
			for _, match := range attribute.FindAllStringSubmatch(element, -1) {
				if hasCJK(match[2]) && !strings.Contains(element, stamps[match[1]]) {
					found = append(found, [2]string{match[1], match[2]})
				}
			}
		}
		return found
	}

	scripts, err := filepath.Glob("web/*.js")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range append([]string{"web/index.html"}, scripts...) {
		if filepath.Base(name) == "i18n.js" {
			continue
		}
		page, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		for _, found := range chineseAttributes(string(page)) {
			if tables[found[0]][found[1]] {
				continue
			}
			t.Fatalf("%s: %s=%q has no way out, stamp the element with %s or add the wording to i18n.js", name, found[0], found[1], stamps[found[0]])
		}
	}

	// 登录页和订阅页不吃 i18n.js，各自带一张 messages 表。写死的中文属性得在表里有条目，
	// applyLanguage 里也得真有同名属性的赋值——少一次，英文界面上就会剩一个只有读屏软件
	// 听得见的中文标签。
	for _, name := range []string{"web/login.html", "web/subscription.html"} {
		page, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		markup := string(page)
		found := chineseAttributes(markup)
		if len(found) == 0 {
			continue
		}
		chinese := i18nBraceBlock(t, markup, strings.Index(markup, "'zh-CN'")+strings.Index(markup[strings.Index(markup, "'zh-CN'"):], "{"), name+" zh-CN messages")
		start := strings.Index(markup, "function applyLanguage")
		body := i18nBraceBlock(t, markup, start+strings.Index(markup[start:], "{"), name+" applyLanguage")
		wanted := map[string]int{}
		for _, hit := range found {
			if !strings.Contains(chinese, "'"+hit[1]+"'") {
				t.Fatalf("%s: %s=%q is missing from the zh-CN messages table", name, hit[0], hit[1])
			}
			wanted[hit[0]]++
		}
		for kind, want := range wanted {
			assignment := "setAttribute('" + kind + "'"
			if kind == "placeholder" {
				assignment = ".placeholder ="
			}
			if got := strings.Count(body, assignment); got < want {
				t.Fatalf("%s: the markup hard-codes %d Chinese %s(s) but applyLanguage only rewrites %d", name, want, kind, got)
			}
		}
	}
}

// i18n.js 的兜底那一遍按原文查表，但 OPTION 在 SKIP_TAGS 里（下拉项常是协议关键字，
// 误翻就把表单值说错了），所以要翻的选项只能靠显式 data-i18n 打标，否则英文面板里
// 会剩一条中文、中文面板里会剩一条 "Default (...)"。语言选单例外：那两项本来就该
// 各自用自己的文字写。
func TestTranslatableOptionsCarryTranslationKeys(t *testing.T) {
	dictionary, err := os.ReadFile("web/i18n.js")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(dictionary), "OPTION: 1") {
		t.Fatal("OPTION left SKIP_TAGS, this guard needs updating")
	}
	option := regexp.MustCompile(`<option([^>]*)>([^<]*)</option>`)
	key := regexp.MustCompile(`data-i18n="([^"]+)"`)
	pages := []string{"web/index.html", "web/login.html", "web/subscription.html"}
	scripts, err := filepath.Glob("web/*.js")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range append(pages, scripts...) {
		page, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		for _, match := range option.FindAllStringSubmatch(string(page), -1) {
			attributes, label := match[1], strings.TrimSpace(match[2])
			// 协议关键字（tcp、bbr、aes-256-gcm）本来就不该翻，会读的只有中文和
			// "Default ..." 这一族"沿用内核默认值"的说明文字。
			translatable := hasCJK(label) || strings.HasPrefix(label, "Default")
			if !translatable || strings.Contains(attributes, `value="zh-CN"`) {
				continue
			}
			found := key.FindStringSubmatch(attributes)
			if found == nil {
				t.Fatalf("%s: <option>%s</option> needs a data-i18n key, i18n.js never rewrites bare options", name, label)
			}
			for _, quoted := range []string{"'" + found[1] + "': '", `"` + found[1] + `": "`} {
				if strings.Count(string(dictionary), quoted) >= 2 {
					found = nil
					break
				}
			}
			if found != nil {
				t.Fatalf("%s: %q needs both a zh-CN and an English entry in web/i18n.js", name, found[1])
			}
		}
	}
}
