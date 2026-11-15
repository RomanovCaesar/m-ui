package app

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
)

// Shadowsocks multi-user listeners.
//
// Mihomo v1.19.34 added `users` to the shadowsocks listener (MetaCubeX/mihomo#3383),
// so several clients can share one port and each connection carries the matched
// username as InUser, which is what per-client traffic accounting keys on.
//
//   - Legacy AEAD ciphers: every client keeps its own password, the top-level
//     password must be empty. Clients need nothing new, so their links do not
//     change when a second client is added.
//   - 2022-blake3-aes-*: the top-level password is a server PSK (iPSK) and every
//     client has a user PSK (uPSK). Clients connect with "iPSK:uPSK". m-ui keeps
//     the server PSK in Inbound.Password; once set, the inbound stays in
//     multi-user mode so the links stay stable.
//   - 2022-blake3-chacha20-poly1305 cannot identify users (no EIH), so it keeps
//     a single client.
//
// With one client and no server PSK, the listener is rendered the old way, which
// every Mihomo version accepts.

// shadowsocksUsersMinCore is the first Mihomo release whose shadowsocks listener
// reads `users`. Older cores silently ignore the key: a legacy AEAD listener then
// fails for lack of a password, and a 2022 listener would accept the bare server
// PSK, which every user's link contains, so disabled users could still connect.
const shadowsocksUsersMinCore = "v1.19.34"

var shadowsocksLegacyAEADCiphers = []string{"aes-128-gcm", "aes-192-gcm", "aes-256-gcm", "chacha20-ietf-poly1305", "xchacha20-ietf-poly1305"}

var shadowsocks2022EIHCiphers = []string{"2022-blake3-aes-128-gcm", "2022-blake3-aes-256-gcm"}

func shadowsocksCipher(inbound Inbound) string {
	return strings.ToLower(strings.TrimSpace(valueOr(inbound.Cipher, "2022-blake3-aes-256-gcm")))
}

// shadowsocksSupportsUsers reports whether Mihomo's listener can tell users apart
// for this cipher.
func shadowsocksSupportsUsers(cipher string) bool {
	return containsString(shadowsocksLegacyAEADCiphers, cipher) || containsString(shadowsocks2022EIHCiphers, cipher)
}

func shadowsocksIs2022EIH(cipher string) bool {
	return containsString(shadowsocks2022EIHCiphers, cipher)
}

// shadowsocksUsersMode reports whether the inbound is rendered with `users`.
func shadowsocksUsersMode(inbound Inbound) bool {
	if inbound.Type != "shadowsocks" {
		return false
	}
	cipher := shadowsocksCipher(inbound)
	if shadowsocksIs2022EIH(cipher) {
		return strings.TrimSpace(inbound.Password) != ""
	}
	return containsString(shadowsocksLegacyAEADCiphers, cipher) && len(inbound.Clients) > 1
}

// shadowsocksClientPassword is the password a client puts into its own config:
// "iPSK:uPSK" for a multi-user 2022 inbound, the client's password otherwise.
func shadowsocksClientPassword(inbound Inbound, client Client) string {
	password := strings.TrimSpace(client.Password)
	if inbound.Type == "shadowsocks" && shadowsocksIs2022EIH(shadowsocksCipher(inbound)) && shadowsocksUsersMode(inbound) && password != "" {
		return strings.TrimSpace(inbound.Password) + ":" + password
	}
	return password
}

// shadowsocks2022KeyLength is the PSK length the 2022 AES ciphers require.
func shadowsocks2022KeyLength(cipher string) int {
	if strings.Contains(cipher, "aes-128") {
		return 16
	}
	return 32
}

func randomShadowsocks2022Key(cipher string) (string, error) {
	key := make([]byte, shadowsocks2022KeyLength(cipher))
	if _, err := rand.Read(key); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(key), nil
}

func validShadowsocks2022Key(cipher, key string) bool {
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(key))
	return err == nil && len(decoded) == shadowsocks2022KeyLength(cipher)
}

// normalizeShadowsocksInbound replaces the single-secret normalization for
// shadowsocks. It moves a legacy top-level password into the sole client,
// keeps the server PSK only where it means something, and generates one when a
// 2022 inbound gets its second client.
func normalizeShadowsocksInbound(inbound *Inbound) error {
	if inbound == nil || inbound.Type != "shadowsocks" {
		return nil
	}
	inbound.Password = strings.TrimSpace(inbound.Password)
	if len(inbound.Clients) == 0 && inbound.Password != "" {
		name := valueOr(inbound.Username, valueOr(inbound.Name, "default"))
		inbound.Clients = []Client{{Name: name, Username: name, Password: inbound.Password, Enabled: true, CreatedAt: inbound.CreatedAt}}
		inbound.Password = ""
	}
	inbound.Username = ""
	inbound.UUID = ""
	cipher := shadowsocksCipher(*inbound)
	if !shadowsocksIs2022EIH(cipher) {
		inbound.Password = ""
		return nil
	}
	if inbound.Password != "" && len(inbound.Clients) <= 1 && !validShadowsocks2022Key(cipher, inbound.Password) {
		// A leftover from the time the inbound itself held the password.
		inbound.Password = ""
	}
	if inbound.Password == "" && len(inbound.Clients) > 1 {
		key, err := randomShadowsocks2022Key(cipher)
		if err != nil {
			return err
		}
		inbound.Password = key
	}
	return nil
}

// validateShadowsocksClients checks the client list against what the cipher
// and the listener mode allow.
func validateShadowsocksClients(inbound Inbound) error {
	cipher := shadowsocksCipher(inbound)
	if len(inbound.Clients) == 0 {
		return fmt.Errorf("Shadowsocks 至少需要一个客户端")
	}
	if len(inbound.Clients) > 1 && !shadowsocksSupportsUsers(cipher) {
		return fmt.Errorf("Shadowsocks 加密方式 %s 不支持多用户，只能有一个客户端；多用户请改用 2022-blake3-aes-* 或 AEAD 加密", cipher)
	}
	enabled := 0
	for _, client := range inbound.Clients {
		if strings.TrimSpace(client.Password) == "" {
			return fmt.Errorf("Shadowsocks 客户端 %s 没有密码", valueOr(client.Name, client.Username))
		}
		if client.Enabled {
			enabled++
		}
	}
	if enabled == 0 {
		return fmt.Errorf("Shadowsocks 至少需要一个启用且有密码的客户端")
	}
	if !shadowsocksUsersMode(inbound) {
		if len(inbound.Clients) > 1 {
			return fmt.Errorf("多用户 Shadowsocks（%s）需要服务端 PSK", cipher)
		}
		return nil
	}
	if shadowsocksIs2022EIH(cipher) {
		if !validShadowsocks2022Key(cipher, inbound.Password) {
			return fmt.Errorf("Shadowsocks 服务端 PSK 必须是 %d 字节密钥的 Base64 编码", shadowsocks2022KeyLength(cipher))
		}
		serverPSK := strings.TrimSpace(inbound.Password)
		seen := map[string]string{}
		for _, client := range inbound.Clients {
			name := valueOr(client.Name, client.Username)
			key := strings.TrimSpace(client.Password)
			if !validShadowsocks2022Key(cipher, key) {
				return fmt.Errorf("Shadowsocks 客户端 %s 的密码必须是 %d 字节密钥的 Base64 编码（多用户的用户 PSK）", name, shadowsocks2022KeyLength(cipher))
			}
			if key == serverPSK {
				return fmt.Errorf("Shadowsocks 客户端 %s 的密码与服务端 PSK 相同", name)
			}
			if other, ok := seen[key]; ok {
				return fmt.Errorf("Shadowsocks 客户端 %s 的密码与 %s 相同，多用户时每个 PSK 必须不同", name, other)
			}
			seen[key] = name
		}
		return nil
	}
	seen := map[string]string{}
	for _, client := range inbound.Clients {
		name := valueOr(client.Name, client.Username)
		key := strings.TrimSpace(client.Password)
		if other, ok := seen[key]; ok {
			return fmt.Errorf("Shadowsocks 客户端 %s 的密码与 %s 相同，多用户时每个密码必须不同", name, other)
		}
		seen[key] = name
	}
	return nil
}

// coreVersionBefore reports whether a release version such as "v1.19.33" is
// older than min. Versions it cannot read (alpha builds, "Unknown") are not
// treated as older, so a build from source is not blocked.
func coreVersionBefore(version, min string) bool {
	have, ok := parseCoreVersion(version)
	if !ok {
		return false
	}
	want, _ := parseCoreVersion(min)
	for index := range want {
		if have[index] != want[index] {
			return have[index] < want[index]
		}
	}
	return false
}

func parseCoreVersion(version string) ([3]int, bool) {
	var parts [3]int
	version = strings.TrimPrefix(strings.TrimSpace(version), "v")
	if cut := strings.IndexAny(version, "-+ "); cut >= 0 {
		version = version[:cut]
	}
	fields := strings.Split(version, ".")
	if len(fields) != 3 {
		return parts, false
	}
	for index, field := range fields {
		number, err := strconv.Atoi(field)
		if err != nil {
			return parts, false
		}
		parts[index] = number
	}
	return parts, true
}

// checkShadowsocksUsersCore refuses multi-user shadowsocks inbounds on a core
// that predates `users`.
func checkShadowsocksUsersCore(inbounds []Inbound, coreVersion string) error {
	if !coreVersionBefore(coreVersion, shadowsocksUsersMinCore) {
		return nil
	}
	for _, inbound := range inbounds {
		if shadowsocksUsersMode(inbound) {
			return fmt.Errorf("多用户 Shadowsocks 入口 %s 需要 Mihomo %s 或更高版本（当前 %s），请先在仪表盘切换内核版本", inbound.Name, shadowsocksUsersMinCore, coreVersion)
		}
	}
	return nil
}

func init() {
	registerServerMessages(map[string][10]string{
		"Shadowsocks 至少需要一个客户端": {"Shadowsocks needs at least one client", "Shadowsocks 至少需要一個用戶端", "Shadowsocks には少なくとも 1 つのクライアントが必要です", "Shadowsocks нужен хотя бы один клиент", "Shadowsocks دست‌کم به یک کلاینت نیاز دارد", "Shadowsocks cần ít nhất một máy khách", "Shadowsocks necesita al menos un cliente", "Shadowsocks en az bir istemci gerektirir", "Shadowsocks потрібен принаймні один клієнт", "O Shadowsocks precisa de pelo menos um cliente"},
	}, []serverMessagePattern{
		{`^Shadowsocks 加密方式 (\S+) 不支持多用户，只能有一个客户端；多用户请改用 2022-blake3-aes-\* 或 AEAD 加密$`, "The Shadowsocks cipher $1 cannot tell users apart and allows one client only; use 2022-blake3-aes-* or an AEAD cipher for several clients", [9]string{"Shadowsocks 加密方式 $1 無法區分使用者，只能有一個用戶端；多個用戶端請改用 2022-blake3-aes-* 或 AEAD 加密", "Shadowsocks の暗号方式 $1 はユーザーを区別できないため、クライアントは 1 つだけです。複数クライアントには 2022-blake3-aes-* または AEAD 暗号を使ってください", "Шифр Shadowsocks $1 не различает пользователей и допускает одного клиента; для нескольких клиентов используйте 2022-blake3-aes-* или шифр AEAD", "رمزنگاری Shadowsocks $1 کاربران را از هم تشخیص نمی‌دهد و فقط یک کلاینت مجاز است؛ برای چند کلاینت از 2022-blake3-aes-* یا رمزنگاری AEAD استفاده کنید", "Mã hóa Shadowsocks $1 không phân biệt được người dùng nên chỉ cho phép một máy khách; với nhiều máy khách hãy dùng 2022-blake3-aes-* hoặc mã hóa AEAD", "El cifrado de Shadowsocks $1 no distingue usuarios y solo admite un cliente; para varios clientes usa 2022-blake3-aes-* o un cifrado AEAD", "Shadowsocks şifrelemesi $1 kullanıcıları ayırt edemez ve yalnızca bir istemciye izin verir; birden çok istemci için 2022-blake3-aes-* veya bir AEAD şifrelemesi kullanın", "Шифр Shadowsocks $1 не розрізняє користувачів і дозволяє одного клієнта; для кількох клієнтів використовуйте 2022-blake3-aes-* або шифр AEAD", "A cifra $1 do Shadowsocks não distingue usuários e permite só um cliente; para vários clientes use 2022-blake3-aes-* ou uma cifra AEAD"}},
		{`^Shadowsocks 加密方式 (\S+) 只支持一个客户端$`, "The Shadowsocks cipher $1 allows one client only", [9]string{"Shadowsocks 加密方式 $1 只支援一個用戶端", "Shadowsocks の暗号方式 $1 はクライアント 1 つのみです", "Шифр Shadowsocks $1 допускает только одного клиента", "رمزنگاری Shadowsocks $1 فقط یک کلاینت را مجاز می‌داند", "Mã hóa Shadowsocks $1 chỉ cho phép một máy khách", "El cifrado de Shadowsocks $1 solo admite un cliente", "Shadowsocks şifrelemesi $1 yalnızca bir istemciye izin verir", "Шифр Shadowsocks $1 дозволяє лише одного клієнта", "A cifra $1 do Shadowsocks permite só um cliente"}},
		{`^Shadowsocks 客户端 (.+) 没有密码$`, "The Shadowsocks client $1 has no password", [9]string{"Shadowsocks 用戶端 $1 沒有密碼", "Shadowsocks クライアント $1 にパスワードがありません", "У клиента Shadowsocks $1 нет пароля", "کلاینت Shadowsocks $1 رمز ندارد", "Máy khách Shadowsocks $1 chưa có mật khẩu", "El cliente de Shadowsocks $1 no tiene contraseña", "Shadowsocks istemcisi $1 için şifre yok", "У клієнта Shadowsocks $1 немає пароля", "O cliente Shadowsocks $1 não tem senha"}},
		{`^多用户 Shadowsocks（(\S+)）需要服务端 PSK$`, "A multi-user Shadowsocks inbound ($1) needs a server PSK", [9]string{"多使用者 Shadowsocks（$1）需要伺服器 PSK", "マルチユーザーの Shadowsocks（$1）にはサーバー PSK が必要です", "Многопользовательскому Shadowsocks ($1) нужен PSK сервера", "Shadowsocks چندکاربره ($1) به PSK سرور نیاز دارد", "Shadowsocks nhiều người dùng ($1) cần PSK máy chủ", "Un Shadowsocks multiusuario ($1) necesita una PSK del servidor", "Çok kullanıcılı Shadowsocks ($1) bir sunucu PSK gerektirir", "Багатокористувацькому Shadowsocks ($1) потрібен PSK сервера", "Um Shadowsocks multiusuário ($1) precisa de uma PSK do servidor"}},
		{`^Shadowsocks 服务端 PSK 必须是 (\d+) 字节密钥的 Base64 编码$`, "The Shadowsocks server PSK must be a Base64-encoded $1-byte key", [9]string{"Shadowsocks 伺服器 PSK 必須是 $1 位元組金鑰的 Base64 編碼", "Shadowsocks のサーバー PSK は $1 バイトの鍵を Base64 にしたものでなければなりません", "PSK сервера Shadowsocks должен быть ключом длиной $1 байт в Base64", "PSK سرور Shadowsocks باید کلید $1 بایتی با کدگذاری Base64 باشد", "PSK máy chủ Shadowsocks phải là khóa $1 byte mã hóa Base64", "La PSK del servidor de Shadowsocks debe ser una clave de $1 bytes en Base64", "Shadowsocks sunucu PSK değeri $1 baytlık bir anahtarın Base64 kodlaması olmalıdır", "PSK сервера Shadowsocks має бути ключем довжиною $1 байт у Base64", "A PSK do servidor Shadowsocks deve ser uma chave de $1 bytes em Base64"}},
		{`^Shadowsocks 客户端 (.+) 的密码必须是 (\d+) 字节密钥的 Base64 编码（多用户的用户 PSK）$`, "The password of the Shadowsocks client $1 must be a Base64-encoded $2-byte key (the user PSK of a multi-user inbound)", [9]string{"Shadowsocks 用戶端 $1 的密碼必須是 $2 位元組金鑰的 Base64 編碼（多使用者的使用者 PSK）", "Shadowsocks クライアント $1 のパスワードは $2 バイトの鍵を Base64 にしたもの（マルチユーザーのユーザー PSK）でなければなりません", "Пароль клиента Shadowsocks $1 должен быть ключом длиной $2 байт в Base64 (PSK пользователя многопользовательского входа)", "رمز کلاینت Shadowsocks $1 باید کلید $2 بایتی با کدگذاری Base64 باشد (PSK کاربر در حالت چندکاربره)", "Mật khẩu của máy khách Shadowsocks $1 phải là khóa $2 byte mã hóa Base64 (PSK người dùng khi nhiều người dùng)", "La contraseña del cliente de Shadowsocks $1 debe ser una clave de $2 bytes en Base64 (la PSK de usuario en modo multiusuario)", "Shadowsocks istemcisi $1 şifresi $2 baytlık bir anahtarın Base64 kodlaması olmalıdır (çok kullanıcılı modda kullanıcı PSK)", "Пароль клієнта Shadowsocks $1 має бути ключем довжиною $2 байт у Base64 (PSK користувача багатокористувацького входу)", "A senha do cliente Shadowsocks $1 deve ser uma chave de $2 bytes em Base64 (a PSK de usuário no modo multiusuário)"}},
		{`^Shadowsocks 客户端 (.+) 的密码与服务端 PSK 相同$`, "The password of the Shadowsocks client $1 is the same as the server PSK", [9]string{"Shadowsocks 用戶端 $1 的密碼與伺服器 PSK 相同", "Shadowsocks クライアント $1 のパスワードがサーバー PSK と同じです", "Пароль клиента Shadowsocks $1 совпадает с PSK сервера", "رمز کلاینت Shadowsocks $1 با PSK سرور یکی است", "Mật khẩu của máy khách Shadowsocks $1 trùng với PSK máy chủ", "La contraseña del cliente de Shadowsocks $1 es igual a la PSK del servidor", "Shadowsocks istemcisi $1 şifresi sunucu PSK ile aynı", "Пароль клієнта Shadowsocks $1 збігається з PSK сервера", "A senha do cliente Shadowsocks $1 é igual à PSK do servidor"}},
		{`^Shadowsocks 客户端 (.+) 的密码与 (.+) 相同，多用户时每个 PSK 必须不同$`, "The Shadowsocks clients $1 and $2 have the same password; every user PSK must be different", [9]string{"Shadowsocks 用戶端 $1 與 $2 的密碼相同，多使用者時每個 PSK 都必須不同", "Shadowsocks クライアント $1 と $2 のパスワードが同じです。ユーザー PSK はすべて異なる必要があります", "У клиентов Shadowsocks $1 и $2 одинаковый пароль; все PSK пользователей должны различаться", "کلاینت‌های Shadowsocks $1 و $2 رمز یکسانی دارند؛ PSK هر کاربر باید متفاوت باشد", "Máy khách Shadowsocks $1 và $2 có cùng mật khẩu; PSK của mỗi người dùng phải khác nhau", "Los clientes de Shadowsocks $1 y $2 tienen la misma contraseña; cada PSK de usuario debe ser distinta", "Shadowsocks istemcileri $1 ve $2 aynı şifreye sahip; her kullanıcı PSK farklı olmalıdır", "У клієнтів Shadowsocks $1 і $2 однаковий пароль; усі PSK користувачів мають відрізнятися", "Os clientes Shadowsocks $1 e $2 têm a mesma senha; cada PSK de usuário deve ser diferente"}},
		{`^Shadowsocks 客户端 (.+) 的密码与 (.+) 相同，多用户时每个密码必须不同$`, "The Shadowsocks clients $1 and $2 have the same password; with several clients every password must be different", [9]string{"Shadowsocks 用戶端 $1 與 $2 的密碼相同，多使用者時每個密碼都必須不同", "Shadowsocks クライアント $1 と $2 のパスワードが同じです。複数クライアントではパスワードをすべて変える必要があります", "У клиентов Shadowsocks $1 и $2 одинаковый пароль; при нескольких клиентах пароли должны различаться", "کلاینت‌های Shadowsocks $1 و $2 رمز یکسانی دارند؛ با چند کلاینت هر رمز باید متفاوت باشد", "Máy khách Shadowsocks $1 và $2 có cùng mật khẩu; khi có nhiều máy khách mỗi mật khẩu phải khác nhau", "Los clientes de Shadowsocks $1 y $2 tienen la misma contraseña; con varios clientes cada contraseña debe ser distinta", "Shadowsocks istemcileri $1 ve $2 aynı şifreye sahip; birden çok istemcide her şifre farklı olmalıdır", "У клієнтів Shadowsocks $1 і $2 однаковий пароль; за кількох клієнтів паролі мають відрізнятися", "Os clientes Shadowsocks $1 e $2 têm a mesma senha; com vários clientes cada senha deve ser diferente"}},
		{`^多用户 Shadowsocks 入口 (.+) 需要 Mihomo (\S+) 或更高版本（当前 (\S+)），请先在仪表盘切换内核版本$`, "Inbound $1 is a multi-user Shadowsocks inbound and needs Mihomo $2 or later (installed: $3); switch the core version on the dashboard first", [9]string{"入站 $1 是多使用者 Shadowsocks，需要 Mihomo $2 或更新版本（目前 $3），請先在儀表板切換核心版本", "インバウンド $1 はマルチユーザーの Shadowsocks のため Mihomo $2 以降が必要です（現在 $3）。先にダッシュボードでコアのバージョンを切り替えてください", "Вход $1 — многопользовательский Shadowsocks, ему нужен Mihomo $2 или новее (сейчас $3); сначала смените версию ядра на панели", "ورودی $1 یک Shadowsocks چندکاربره است و به Mihomo نسخهٔ $2 یا جدیدتر نیاز دارد (نسخهٔ فعلی $3)؛ ابتدا نسخهٔ هسته را در داشبورد عوض کنید", "Inbound $1 là Shadowsocks nhiều người dùng và cần Mihomo $2 trở lên (hiện tại $3); hãy đổi phiên bản lõi ở bảng điều khiển trước", "La entrada $1 es un Shadowsocks multiusuario y necesita Mihomo $2 o posterior (actual: $3); cambia primero la versión del núcleo en el panel", "$1 inbound'u çok kullanıcılı bir Shadowsocks'tur ve Mihomo $2 veya üstünü gerektirir (mevcut: $3); önce gösterge panelinden çekirdek sürümünü değiştirin", "Вхід $1 — багатокористувацький Shadowsocks, йому потрібен Mihomo $2 або новіший (зараз $3); спершу змініть версію ядра на панелі", "A entrada $1 é um Shadowsocks multiusuário e precisa do Mihomo $2 ou mais recente (atual: $3); troque primeiro a versão do núcleo no painel"}},
	})
}
