package app

import (
	"regexp"
	"strings"
)

// Lines m-ui writes to the log buffer itself (Mihomo's own output is English).
// They are stored in Chinese and translated when the log is served, like the
// other server messages: a detail after ": " (usually an error) is translated
// on its own.
//
// Order: en, zh-TW, ja, ru, fa, vi, es, tr, uk, pt-BR.
var panelLogMessages = map[string][10]string{
	"Mihomo 自动启动失败":                            {"Mihomo failed to start automatically", "Mihomo 自動啟動失敗", "Mihomo の自動起動に失敗しました", "Не удалось автоматически запустить Mihomo", "راه‌اندازی خودکار Mihomo ناموفق بود", "Không tự khởi động được Mihomo", "Mihomo no pudo iniciarse automáticamente", "Mihomo otomatik olarak başlatılamadı", "Не вдалося автоматично запустити Mihomo", "O Mihomo não conseguiu iniciar automaticamente"},
	"Mihomo 已启动":                               {"Mihomo started", "Mihomo 已啟動", "Mihomo を起動しました", "Mihomo запущен", "Mihomo راه‌اندازی شد", "Mihomo đã khởi động", "Mihomo iniciado", "Mihomo başlatıldı", "Mihomo запущено", "Mihomo iniciado"},
	"Mihomo 已退出":                               {"Mihomo exited", "Mihomo 已結束", "Mihomo が終了しました", "Mihomo завершил работу", "Mihomo خارج شد", "Mihomo đã thoát", "Mihomo se cerró", "Mihomo kapandı", "Mihomo завершило роботу", "O Mihomo foi encerrado"},
	"Mihomo 已停止":                               {"Mihomo stopped", "Mihomo 已停止", "Mihomo を停止しました", "Mihomo остановлен", "Mihomo متوقف شد", "Mihomo đã dừng", "Mihomo detenido", "Mihomo durduruldu", "Mihomo зупинено", "Mihomo parado"},
	"Mihomo 核心已切换":                             {"Mihomo core switched", "Mihomo 核心已切換", "Mihomo コアを切り替えました", "Ядро Mihomo переключено", "هستهٔ Mihomo تعویض شد", "Đã chuyển lõi Mihomo", "Núcleo de Mihomo cambiado", "Mihomo çekirdeği değiştirildi", "Ядро Mihomo перемкнено", "Núcleo do Mihomo trocado"},
	"面板数据已从备份恢复":                               {"Panel data restored from a backup", "面板資料已從備份還原", "パネルのデータをバックアップから復元しました", "Данные панели восстановлены из резервной копии", "داده‌های پنل از نسخهٔ پشتیبان بازیابی شد", "Đã khôi phục dữ liệu bảng điều khiển từ bản sao lưu", "Datos del panel restaurados desde una copia de seguridad", "Panel verileri yedekten geri yüklendi", "Дані панелі відновлено з резервної копії", "Dados do painel restaurados de um backup"},
	"GeoIP.dat、GeoSite.dat 与 geoip.metadb 已更新": {"GeoIP.dat, GeoSite.dat and geoip.metadb updated", "GeoIP.dat、GeoSite.dat 與 geoip.metadb 已更新", "GeoIP.dat、GeoSite.dat、geoip.metadb を更新しました", "GeoIP.dat, GeoSite.dat и geoip.metadb обновлены", "GeoIP.dat، GeoSite.dat و geoip.metadb به‌روزرسانی شدند", "Đã cập nhật GeoIP.dat, GeoSite.dat và geoip.metadb", "GeoIP.dat, GeoSite.dat y geoip.metadb actualizados", "GeoIP.dat, GeoSite.dat ve geoip.metadb güncellendi", "GeoIP.dat, GeoSite.dat і geoip.metadb оновлено", "GeoIP.dat, GeoSite.dat e geoip.metadb atualizados"},
	"应用客户端配额失败":                                {"Applying client limits failed", "套用用戶端配額失敗", "クライアントの上限の適用に失敗しました", "Не удалось применить лимиты клиентов", "اعمال محدودیت‌های کلاینت ناموفق بود", "Áp dụng giới hạn máy khách thất bại", "No se pudieron aplicar los límites de clientes", "İstemci sınırları uygulanamadı", "Не вдалося застосувати ліміти клієнтів", "Falha ao aplicar os limites dos clientes"},
	"应用流量重置失败":                                 {"Applying the traffic reset failed", "套用流量重置失敗", "通信量リセットの適用に失敗しました", "Не удалось применить сброс трафика", "اعمال بازنشانی ترافیک ناموفق بود", "Áp dụng đặt lại lưu lượng thất bại", "No se pudo aplicar el reinicio de tráfico", "Trafik sıfırlaması uygulanamadı", "Не вдалося застосувати скидання трафіку", "Falha ao aplicar a redefinição de tráfego"},
	"外部流量上报失败":                                 {"Reporting traffic to the external endpoint failed", "外部流量回報失敗", "外部への通信量の報告に失敗しました", "Не удалось отправить трафик во внешний сервис", "گزارش ترافیک به سرویس خارجی ناموفق بود", "Gửi báo cáo lưu lượng ra ngoài thất bại", "No se pudo informar el tráfico al servicio externo", "Trafik harici servise bildirilemedi", "Не вдалося надіслати трафік у зовнішній сервіс", "Falha ao informar o tráfego ao serviço externo"},
	"VPNGate 安装失败":                             {"VPNGate installation failed", "VPNGate 安裝失敗", "VPNGate のインストールに失敗しました", "Не удалось установить VPNGate", "نصب VPNGate ناموفق بود", "Cài đặt VPNGate thất bại", "La instalación de VPNGate falló", "VPNGate kurulamadı", "Не вдалося встановити VPNGate", "Falha na instalação do VPNGate"},
	"VPNGate 安装后写入 Mihomo 配置失败":                {"Writing the Mihomo configuration after installing VPNGate failed", "VPNGate 安裝後寫入 Mihomo 設定失敗", "VPNGate のインストール後に Mihomo の設定を書き込めませんでした", "После установки VPNGate не удалось записать конфигурацию Mihomo", "نوشتن پیکربندی Mihomo پس از نصب VPNGate ناموفق بود", "Ghi cấu hình Mihomo sau khi cài VPNGate thất bại", "No se pudo escribir la configuración de Mihomo tras instalar VPNGate", "VPNGate kurulduktan sonra Mihomo yapılandırması yazılamadı", "Після встановлення VPNGate не вдалося записати конфігурацію Mihomo", "Falha ao gravar a configuração do Mihomo após instalar o VPNGate"},
	"VPNGate 服务停止失败":                           {"Stopping the VPNGate service failed", "VPNGate 服務停止失敗", "VPNGate サービスを停止できませんでした", "Не удалось остановить службу VPNGate", "توقف سرویس VPNGate ناموفق بود", "Dừng dịch vụ VPNGate thất bại", "No se pudo detener el servicio VPNGate", "VPNGate hizmeti durdurulamadı", "Не вдалося зупинити службу VPNGate", "Falha ao parar o serviço VPNGate"},
	"VPNGate: 开始编译客户端":                         {"VPNGate: building the client", "VPNGate：開始編譯用戶端", "VPNGate: クライアントをビルドしています", "VPNGate: сборка клиента", "VPNGate: در حال ساخت کلاینت", "VPNGate: đang biên dịch máy khách", "VPNGate: compilando el cliente", "VPNGate: istemci derleniyor", "VPNGate: збирання клієнта", "VPNGate: compilando o cliente"},
}

// serverMessagePattern is a server message with values in it: from matches the
// Chinese text, to is the English result and translated the other languages, in
// the order zh-TW, ja, ru, fa, vi, es, tr, uk, pt-BR.
type serverMessagePattern struct {
	from       string
	to         string
	translated [9]string
}

var panelLogPatterns = []serverMessagePattern{
	{`^Inbound (.+) 已到期或耗尽，正在停止它的监听$`, "Inbound $1 is used up or expired; stopping its listener", [9]string{
		"Inbound $1 已到期或耗盡，正在停止它的監聽", "インバウンド $1 は上限に達したか期限切れのため、リスナーを停止します", "Вход $1 исчерпан или истёк; его слушатель останавливается", "ورودی $1 تمام شده یا منقضی شده است؛ شنوندهٔ آن متوقف می‌شود", "Inbound $1 đã hết lưu lượng hoặc hết hạn; đang dừng listener của nó", "La entrada $1 se agotó o caducó; se detiene su listener", "Giriş $1 tükendi veya süresi doldu; dinleyicisi durduruluyor", "Вхід $1 вичерпано або строк сплив; його слухача зупинено", "A entrada $1 esgotou ou expirou; parando o listener dela"}},
	{`^客户端 (.+) 已到期或耗尽，正在应用认证变更$`, "Client $1 is used up or expired; applying the change", [9]string{
		"用戶端 $1 已到期或耗盡，正在套用驗證變更", "クライアント $1 は上限に達したか期限切れのため、認証の変更を適用します", "Клиент $1 исчерпан или истёк; изменение применяется", "کلاینت $1 تمام شده یا منقضی شده است؛ تغییر اعمال می‌شود", "Máy khách $1 đã hết lưu lượng hoặc hết hạn; đang áp dụng thay đổi", "El cliente $1 se agotó o caducó; aplicando el cambio", "İstemci $1 tükendi veya süresi doldu; değişiklik uygulanıyor", "Клієнта $1 вичерпано або строк сплив; зміна застосовується", "O cliente $1 esgotou ou expirou; aplicando a alteração"}},
	{`^Inbound (.+) 已执行周期流量重置（(\S+)）$`, "Inbound $1: scheduled traffic reset ($2)", [9]string{
		"Inbound $1 已執行週期流量重置（$2）", "インバウンド $1: 定期の通信量リセットを実行しました（$2）", "Вход $1: плановый сброс трафика ($2)", "ورودی $1: بازنشانی زمان‌بندی‌شدهٔ ترافیک ($2)", "Inbound $1: đã đặt lại lưu lượng theo lịch ($2)", "Entrada $1: reinicio de tráfico programado ($2)", "Giriş $1: zamanlanmış trafik sıfırlaması ($2)", "Вхід $1: планове скидання трафіку ($2)", "Entrada $1: redefinição de tráfego programada ($2)"}},
	{`^客户端 (.+) 已执行周期流量重置（(\S+)）$`, "Client $1: scheduled traffic reset ($2)", [9]string{
		"用戶端 $1 已執行週期流量重置（$2）", "クライアント $1: 定期の通信量リセットを実行しました（$2）", "Клиент $1: плановый сброс трафика ($2)", "کلاینت $1: بازنشانی زمان‌بندی‌شدهٔ ترافیک ($2)", "Máy khách $1: đã đặt lại lưu lượng theo lịch ($2)", "Cliente $1: reinicio de tráfico programado ($2)", "İstemci $1: zamanlanmış trafik sıfırlaması ($2)", "Клієнт $1: планове скидання трафіку ($2)", "Cliente $1: redefinição de tráfego programada ($2)"}},
	{`^客户端 (.+) 已自动续期至 (.+)（第 (\d+) 次）$`, "Client $1 renewed until $2 (renewal $3)", [9]string{
		"用戶端 $1 已自動續期至 $2（第 $3 次）", "クライアント $1 を $2 まで自動更新しました（$3 回目）", "Клиент $1 продлён до $2 (продление № $3)", "کلاینت $1 تا $2 تمدید شد (تمدید $3)", "Máy khách $1 đã được gia hạn đến $2 (lần $3)", "Cliente $1 renovado hasta $2 (renovación $3)", "İstemci $1, $2 tarihine kadar yenilendi ($3. yenileme)", "Клієнта $1 подовжено до $2 (подовження № $3)", "Cliente $1 renovado até $2 (renovação $3)"}},
	{`^(\S+) 已通过 jsDelivr 备用线路下载$`, "$1 was downloaded through the jsDelivr mirror", [9]string{
		"$1 已透過 jsDelivr 備用線路下載", "$1 を jsDelivr のミラーからダウンロードしました", "$1 загружен через зеркало jsDelivr", "$1 از طریق آینهٔ jsDelivr دانلود شد", "Đã tải $1 qua bản sao jsDelivr", "$1 se descargó desde el espejo de jsDelivr", "$1 jsDelivr yansısından indirildi", "$1 завантажено через дзеркало jsDelivr", "$1 foi baixado pelo espelho do jsDelivr"}},
	{`^下载 (\S+) 线路 (\d+) 失败$`, "Downloading $1 from source $2 failed", [9]string{
		"下載 $1 線路 $2 失敗", "$1 をダウンロード元 $2 から取得できませんでした", "Не удалось загрузить $1 из источника $2", "دانلود $1 از منبع $2 ناموفق بود", "Tải $1 từ nguồn $2 thất bại", "No se pudo descargar $1 desde la fuente $2", "$1, $2 numaralı kaynaktan indirilemedi", "Не вдалося завантажити $1 із джерела $2", "Falha ao baixar $1 da fonte $2"}},
	{`^VPNGate: 开始下载 (.+)$`, "VPNGate: downloading $1", [9]string{
		"VPNGate：開始下載 $1", "VPNGate: $1 をダウンロードしています", "VPNGate: загрузка $1", "VPNGate: در حال دانلود $1", "VPNGate: đang tải $1", "VPNGate: descargando $1", "VPNGate: $1 indiriliyor", "VPNGate: завантаження $1", "VPNGate: baixando $1"}},
	{`^VPNGate: 客户端安装完成 (.+)$`, "VPNGate: client $1 installed", [9]string{
		"VPNGate：用戶端安裝完成 $1", "VPNGate: クライアント $1 をインストールしました", "VPNGate: клиент $1 установлен", "VPNGate: کلاینت $1 نصب شد", "VPNGate: đã cài máy khách $1", "VPNGate: cliente $1 instalado", "VPNGate: istemci $1 kuruldu", "VPNGate: клієнт $1 встановлено", "VPNGate: cliente $1 instalado"}},
	{`^VPNGate 槽位 (\d+) 禁用网卡 (\S+) 失败，继续尝试删除$`, "VPNGate slot $1: disabling adapter $2 failed, deleting it anyway", [9]string{
		"VPNGate 槽位 $1 停用網卡 $2 失敗，繼續嘗試刪除", "VPNGate スロット $1: アダプター $2 を無効にできなかったため、削除を続けます", "VPNGate слот $1: не удалось отключить адаптер $2, он всё равно удаляется", "VPNGate جایگاه $1: غیرفعال کردن آداپتور $2 ناموفق بود، حذف آن ادامه می‌یابد", "VPNGate khe $1: không tắt được bộ điều hợp $2, vẫn tiếp tục xóa", "VPNGate ranura $1: no se pudo desactivar el adaptador $2; se elimina de todos modos", "VPNGate yuva $1: $2 bağdaştırıcısı devre dışı bırakılamadı, yine de siliniyor", "VPNGate слот $1: не вдалося вимкнути адаптер $2, його все одно видаляють", "VPNGate slot $1: falha ao desativar o adaptador $2; excluindo mesmo assim"}},
	{`^VPNGate 槽位 (\d+) 清理失败$`, "VPNGate slot $1: cleanup failed", [9]string{
		"VPNGate 槽位 $1 清理失敗", "VPNGate スロット $1: 後片付けに失敗しました", "VPNGate слот $1: очистка не удалась", "VPNGate جایگاه $1: پاک‌سازی ناموفق بود", "VPNGate khe $1: dọn dẹp thất bại", "VPNGate ranura $1: la limpieza falló", "VPNGate yuva $1: temizlik başarısız", "VPNGate слот $1: очищення не вдалося", "VPNGate slot $1: a limpeza falhou"}},
	{`^VPNGate 槽位 (\d+) 已连接 (.+)$`, "VPNGate slot $1 connected to $2", [9]string{
		"VPNGate 槽位 $1 已連線 $2", "VPNGate スロット $1 が $2 に接続しました", "VPNGate слот $1 подключён к $2", "VPNGate جایگاه $1 به $2 وصل شد", "VPNGate khe $1 đã kết nối tới $2", "VPNGate ranura $1 conectada a $2", "VPNGate yuva $1, $2 adresine bağlandı", "VPNGate слот $1 підключено до $2", "VPNGate slot $1 conectado a $2"}},
	{`^VPNGate 槽位 (\d+)$`, "VPNGate slot $1", [9]string{
		"VPNGate 槽位 $1", "VPNGate スロット $1", "VPNGate слот $1", "VPNGate جایگاه $1", "VPNGate khe $1", "VPNGate ranura $1", "VPNGate yuva $1", "VPNGate слот $1", "VPNGate slot $1"}},
}

func init() {
	registerServerMessages(panelLogMessages, panelLogPatterns)
}

// registerServerMessages adds fixed messages (en, zh-TW, ja, ru, fa, vi, es, tr,
// uk, pt-BR) and patterns to the catalogs translateMessage reads.
func registerServerMessages(messages map[string][10]string, patterns []serverMessagePattern) {
	languages := []string{"zh-TW", "ja", "ru", "fa", "vi", "es", "tr", "uk", "pt-BR"}
	for source, translations := range messages {
		messagesEN[source] = translations[0]
		for i, language := range languages {
			messagesLocalized[language][source] = translations[i+1]
		}
	}
	for _, pattern := range patterns {
		messagePatternsEN = append(messagePatternsEN, struct {
			from *regexp.Regexp
			to   string
		}{regexp.MustCompile(pattern.from), pattern.to})
		for i, language := range languages {
			messagePatternsLocalized[language] = append(messagePatternsLocalized[language], pattern.translated[i])
		}
	}
}

var logTimestamp = regexp.MustCompile(`^(\d\d:\d\d:\d\d  )(.*)$`)

// translateLogs translates the panel's own lines of the log buffer; Mihomo's
// output passes through unchanged.
func translateLogs(lines []string, language string) []string {
	if language == defaultLanguage {
		return lines
	}
	translated := make([]string, len(lines))
	for i, line := range lines {
		translated[i] = line
		if !hasCJK(line) {
			continue
		}
		if match := logTimestamp.FindStringSubmatch(line); match != nil {
			translated[i] = match[1] + translateMessage(match[2], language)
		} else {
			translated[i] = translateMessage(strings.TrimSpace(line), language)
		}
	}
	return translated
}
