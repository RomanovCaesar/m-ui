#!/usr/bin/env bash
set -Eeuo pipefail
umask 077

INSTALL_DIR="${MUI_INSTALL_DIR:-/usr/local/m-ui}"
BIN="${INSTALL_DIR}/m-ui"
DATA_DIR="${INSTALL_DIR}/data"
INSTALL_URL="https://raw.githubusercontent.com/RomanovCaesar/m-ui/main/install.sh"
# 菜单语言和面板界面语言保持同一套；m-ui --lang <code> 选好后记在这个文件里。
MENU_LANG_FILE="${INSTALL_DIR}/menu-lang"
MENU_LANGS=(en zh-CN zh-TW ja ru fa vi es tr uk pt-BR)
MENU_LANG="en"
red='\033[0;31m'; green='\033[0;32m'; yellow='\033[0;33m'; blue='\033[0;34m'; plain='\033[0m'
[[ -t 1 ]] || red='' green='' yellow='' blue='' plain=''

declare -A MSG=()
M() { MSG[$1]="$2"; }
t() { printf '%s' "${MSG[$1]:-$1}"; }
# shellcheck disable=SC2059
tf() { local format="${MSG[$1]:-$1}"; shift; printf "$format" "$@"; }

info() { echo -e "${green}[m-ui]${plain} $*"; }
warn() { echo -e "${yellow}[m-ui]${plain} $*"; }
die() { echo -e "${red}[m-ui] $(t err_prefix)${plain} $*" >&2; exit 1; }
require_root() { [[ ${EUID} -eq 0 ]] || die "$(t need_root)"; }

has_systemd() { command -v systemctl >/dev/null 2>&1 && [[ -d /run/systemd/system ]]; }
service_do() { if has_systemd; then systemctl "$1" m-ui; else rc-service m-ui "$1"; fi; }
start() { require_root; service_do start; }
stop() { require_root; service_do stop; }
restart() { require_root; service_do restart; }
status() { if has_systemd; then systemctl status m-ui --no-pager; else rc-service m-ui status; fi; }
logs() { if has_systemd; then journalctl -u m-ui -e --no-pager -n 200; else tail -n 200 /var/log/m-ui.log; fi; }
enable() { require_root; if has_systemd; then systemctl enable m-ui; else rc-update add m-ui default; fi; }
disable() { require_root; if has_systemd; then systemctl disable m-ui; else rc-update del m-ui default; fi; }
settings() { "$BIN" settings --data-dir "$DATA_DIR"; }
version() { "$BIN" version; }

random_alnum() {
    local length="$1" value
    while true; do
        value="$(openssl rand -base64 $((length * 3)) | LC_ALL=C tr -dc 'A-Za-z0-9' | cut -c "1-${length}")"
        [[ ${#value} -eq $length && "$value" =~ [A-Z] && "$value" =~ [a-z] && "$value" =~ [0-9] ]] && { printf '%s' "$value"; return; }
    done
}

update() {
    require_root
    local script; script="$(mktemp /tmp/m-ui-update.XXXXXX.sh)"
    if ! curl -fsSL --retry 3 "$INSTALL_URL" -o "$script"; then
        rm -f -- "$script"
        die "$(t dl_installer_fail)"
    fi
    if ! bash "$script" --update "$@"; then
        rm -f -- "$script"
        return 1
    fi
    rm -f -- "$script"
}

install_cmd() {
    require_root
    local script; script="$(mktemp /tmp/m-ui-install.XXXXXX.sh)"
    if ! curl -fsSL --retry 3 "$INSTALL_URL" -o "$script"; then
        rm -f -- "$script"
        die "$(t dl_installer_fail)"
    fi
    if ! bash "$script" "$@"; then
        rm -f -- "$script"
        return 1
    fi
    rm -f -- "$script"
}

update_menu() {
    require_root
    local menu; menu="$(mktemp /tmp/m-ui-menu.XXXXXX)"
    if ! curl -fsSL --retry 3 "https://raw.githubusercontent.com/RomanovCaesar/m-ui/main/scripts/m-ui.sh" -o "$menu"; then
        rm -f -- "$menu"
        die "$(t dl_menu_fail)"
    fi
    install -m 0755 "$menu" "${INSTALL_DIR}/m-ui.sh"
    ln -sfn "${INSTALL_DIR}/m-ui.sh" /usr/local/bin/m-ui
    rm -f -- "$menu"
    info "$(t menu_updated)"
}

run_ssl_installer() {
    require_root
    local script; script="$(mktemp /tmp/m-ui-ssl.XXXXXX.sh)"
    if ! curl -fsSL --retry 3 "$INSTALL_URL" -o "$script"; then
        rm -f -- "$script"
        die "$(t dl_installer_fail)"
    fi
    if ! bash -n "$script"; then
        rm -f -- "$script"
        warn "$(t installer_syntax)"
        return 1
    fi
    if ! bash "$script" "$@"; then
        rm -f -- "$script"
        return 1
    fi
    rm -f -- "$script"
}

acme_binary() {
    local acme="/root/.acme.sh/acme.sh"
    [[ -x "$acme" ]] || die "$(t acme_missing)"
    printf '%s' "$acme"
}

ssl_issue_domain() {
    local domain="${1:-}"
    [[ -n "$domain" ]] || read -rp "$(t p_domain)" domain
    [[ -n "$domain" ]] || die "$(t domain_empty)"
    run_ssl_installer --ssl-menu --ssl-domain "$domain"
}

ssl_issue_ip() {
    local address="${1:-}"
    if [[ -z "$address" ]]; then
        read -rp "$(t p_ipv4)" address
    fi
    run_ssl_installer --ssl-menu --ssl-ip "$address"
}

ssl_list() {
    if [[ -x /root/.acme.sh/acme.sh ]]; then
        /root/.acme.sh/acme.sh --list
    else
        warn "$(t acme_not_installed)"
    fi
    if [[ -d /root/cert ]]; then
        echo
        t cert_files; echo
        find /root/cert -mindepth 1 -maxdepth 2 -type f \( -name fullchain.pem -o -name privkey.pem \) -print | sort
    fi
}

ssl_select_domain() {
    local prompt="$1" acme domain
    acme="$(acme_binary)"
    "$acme" --list >&2
    read -rp "$prompt" domain
    [[ -n "$domain" ]] || return 1
    "$acme" --list 2>/dev/null | awk 'NR > 1 {print $1}' | grep -Fxq -- "$domain" || {
        warn "$(tf domain_unmanaged "$domain")"
        return 1
    }
    printf '%s' "$domain"
}

ssl_revoke() {
    local acme domain
    acme="$(acme_binary)"
    domain="$(ssl_select_domain "$(t p_revoke)")" || return 1
    "$acme" --revoke -d "$domain"
}

ssl_renew() {
    local acme domain
    acme="$(acme_binary)"
    domain="$(ssl_select_domain "$(t p_renew)")" || return 1
    "$acme" --renew -d "$domain" --force
    restart
}

ssl_set_paths() {
    local cert="${1:-}" key="${2:-}" domain="${3:-}"
    [[ -n "$cert" ]] || read -rp "$(t p_cert)" cert
    [[ -n "$key" ]] || read -rp "$(t p_key)" key
    if [[ $# -lt 3 ]]; then read -rp "$(t p_cert_domain)" domain; fi
    [[ -s "$cert" && -r "$cert" ]] || die "$(t cert_bad)"
    [[ -s "$key" && -r "$key" ]] || die "$(t key_bad)"
    local args=(configure --data-dir "$DATA_DIR" --cert "$cert" --key "$key" --domain "$domain")
    "$BIN" "${args[@]}" >/dev/null
    restart
    info "$(t tls_applied)"
}

# ensure_full_chain 确认装出来的证书文件带着中间证书。只有叶子证书时浏览器照样能开，
# 但 Mihomo Party / FlClash 等客户端会报 "unable to verify the first certificate"，
# 所以退回 acme.sh 自己保存的 fullchain.cer。
ensure_full_chain() {
    local ident="$1" cert="$2" candidate
    [[ $(grep -c 'BEGIN CERTIFICATE' "$cert" 2>/dev/null) -ge 2 ]] && return 0
    for candidate in "/root/.acme.sh/${ident}_ecc/fullchain.cer" "/root/.acme.sh/${ident}/fullchain.cer"; do
        if [[ $(grep -c 'BEGIN CERTIFICATE' "$candidate" 2>/dev/null) -ge 2 ]]; then
            cp -f "$candidate" "$cert"
            return 0
        fi
    done
    warn "$(tf chain_missing "$cert")"
    return 1
}

install_acme_for_dns() {
    [[ -x /root/.acme.sh/acme.sh ]] && return 0
    info "$(t acme_installing)"
    if ! (cd /root && curl -fsSL --retry 3 --connect-timeout 10 https://get.acme.sh | sh); then
        warn "$(t acme_dl_fail)"
        return 1
    fi
    [[ -x /root/.acme.sh/acme.sh ]] || { warn "$(t acme_install_fail)"; return 1; }
    info "$(t acme_installed)"
}

ssl_cloudflare() {
    require_root
    install_acme_for_dns || die "$(t acme_install_fail)"
    local domain method secret email="" cert_dir cert key acme="/root/.acme.sh/acme.sh"
    read -rp "$(t p_domain)" domain
    [[ "$domain" =~ ^[A-Za-z0-9]([A-Za-z0-9-]*[A-Za-z0-9])?(\.[A-Za-z0-9]([A-Za-z0-9-]*[A-Za-z0-9])?)+$ ]] || die "$(t domain_invalid)"
    echo "  1. $(t cf_opt_token)"
    echo "  2. $(t cf_opt_key)"
    read -rp "$(t p_choose_12)" method
    case "$method" in
        1)
            read -rsp "$(t p_cf_token)" secret; echo
            [[ -n "$secret" ]] || die "$(t cf_token_empty)"
            export CF_Token="$secret"
            ;;
        2)
            read -rsp "$(t p_cf_key)" secret; echo
            read -rp "$(t p_cf_email)" email
            [[ -n "$secret" && -n "$email" ]] || die "$(t cf_key_empty)"
            export CF_Key="$secret" CF_Email="$email"
            ;;
        *) die "$(t invalid_choice)" ;;
    esac
    "$acme" --set-default-ca --server letsencrypt --force >/dev/null 2>&1 || true
    if ! "$acme" --issue --dns dns_cf -d "$domain" -d "*.${domain}" --force; then
        unset CF_Token CF_Key CF_Email
        die "$(t cf_issue_fail)"
    fi
    unset CF_Token CF_Key CF_Email
    cert_dir="/root/cert/${domain}"; cert="${cert_dir}/fullchain.pem"; key="${cert_dir}/privkey.pem"
    mkdir -p "$cert_dir"; chmod 0700 "$cert_dir"
    "$acme" --installcert -d "$domain" --key-file "$key" --fullchain-file "$cert" --reloadcmd "systemctl restart m-ui 2>/dev/null || rc-service m-ui restart 2>/dev/null || true" || true
    [[ -s "$cert" && -s "$key" ]] || die "$(t acme_no_files)"
    ensure_full_chain "$domain" "$cert" || true
    chmod 0644 "$cert"; chmod 0600 "$key"
    "$acme" --upgrade --auto-upgrade >/dev/null 2>&1 || warn "$(t acme_upgrade_fail)"
    ssl_set_paths "$cert" "$key" "$domain"
}

ssl() {
    require_root
    local ssl_command="${1:-menu}" ssl_choice="" i
    case "$ssl_command" in
        domain) shift; ssl_issue_domain "$@"; return ;;
        ip) shift; ssl_issue_ip "$@"; return ;;
        cloudflare|cf) shift; ssl_cloudflare "$@"; return ;;
        renew) shift; ssl_renew "$@"; return ;;
        revoke) shift; ssl_revoke "$@"; return ;;
        list) shift; ssl_list "$@"; return ;;
        set) shift; ssl_set_paths "$@"; return ;;
        menu) ;;
        *) die "$(tf tls_unknown "$1")" ;;
    esac
    t tls_title; echo
    for i in 1 2 3 4 5 6 7; do echo "  ${i}. $(t "tls_${i}")"; done
    echo "  0. $(t back)"
    read -rp "$(t p_choose_07)" ssl_choice
    case "$ssl_choice" in
        1) ssl_issue_domain;; 2) ssl_issue_ip;; 3) ssl_cloudflare;; 4) ssl_renew;;
        5) ssl_revoke;; 6) ssl_list;; 7) ssl_set_paths;; 0) return 0;; *) die "$(t invalid_choice)";;
    esac
}

set_port() {
    require_root
    local port="${1:-}"
    if [[ -z "$port" ]]; then read -rp "$(t p_port)" port; fi
    [[ "$port" =~ ^[0-9]+$ ]] || die "$(t port_invalid)"
    "$BIN" configure --data-dir "$DATA_DIR" --port "$port"
    restart
}

clear_logs() {
    require_root
    if has_systemd; then
        journalctl --rotate >/dev/null 2>&1 || true
        journalctl --vacuum-time=1s >/dev/null 2>&1 || true
    elif [[ -f /var/log/m-ui.log ]]; then
        : > /var/log/m-ui.log
    fi
    info "$(t logs_cleared)"
}

configure() {
    require_root
    local current port path username password args
    current="$(settings)"
    echo "$current"
    read -rp "$(t p_keep_port)" port
    read -rp "$(t p_keep_path)" path
    read -rp "$(t p_keep_user)" username
    read -rsp "$(t p_keep_pass)" password; echo
    args=(configure --data-dir "$DATA_DIR")
    [[ -n "$port" ]] && args+=(--port "$port")
    [[ -n "$path" ]] && args+=(--path "$path")
    [[ -n "$username" ]] && args+=(--username "$username")
    if [[ -n "$password" ]]; then printf '%s' "$password" | "$BIN" "${args[@]}" --password-stdin
    else "$BIN" "${args[@]}"
    fi
    restart
}

reset_credentials() {
    require_root
    local username password
    read -rp "$(t p_new_user)" username
    [[ -n "$username" ]] || username="$(random_alnum 14)"
    read -rsp "$(t p_new_pass)" password; echo
    [[ -n "$password" ]] || password="$(random_alnum 16)"
    printf '%s' "$password" | "$BIN" configure --data-dir "$DATA_DIR" --username "$username" --password-stdin >/dev/null
    restart
    info "$(tf show_user "$username")"
    info "$(tf show_pass "$password")"
}

reset_path() {
    require_root
    local path; path="$(random_alnum 16)"
    "$BIN" configure --data-dir "$DATA_DIR" --path "$path" >/dev/null
    restart
    info "$(tf show_path "/${path}/")"
}

uninstall() {
    require_root
    read -rp "$(t p_uninstall)" answer
    [[ "$answer" =~ ^[Yy]$ ]] || { warn "$(t cancelled)"; return; }
    if has_systemd; then
        systemctl disable --now m-ui >/dev/null 2>&1 || true
        rm -f /etc/systemd/system/m-ui.service
        systemctl daemon-reload
        systemctl reset-failed >/dev/null 2>&1 || true
    else
        rc-service m-ui stop >/dev/null 2>&1 || true
        rc-update del m-ui default >/dev/null 2>&1 || true
        rm -f /etc/init.d/m-ui
    fi
    rm -f /usr/local/bin/m-ui
    read -rp "$(tf p_purge "$DATA_DIR")" purge
    if [[ "$purge" =~ ^[Yy]$ ]]; then
        [[ "$INSTALL_DIR" == "/usr/local/m-ui" ]] || die "$(t purge_refuse)"
        rm -rf -- "$INSTALL_DIR"
        info "$(t removed_all)"
    else
        rm -f -- "$BIN" "${INSTALL_DIR}/mihomo" "${INSTALL_DIR}/m-ui.sh" "$MENU_LANG_FILE"
        info "$(tf removed_keep "$DATA_DIR")"
    fi
}

show_usage() {
    t usage_title; echo
    printf '  %-34s %s\n' \
        "m-ui" "$(t u_menu)" \
        "m-ui start|stop|restart|status" "$(t u_service)" \
        "m-ui logs|clear-logs" "$(t u_logs)" \
        "m-ui settings|configure|set-port" "$(t u_settings)" \
        "m-ui reset-credentials|reset-path" "$(t u_reset)" \
        "m-ui enable|disable" "$(t u_boot)" \
        "m-ui install|update|update-menu" "$(t u_update)" \
        "m-ui ssl <subcommand>" "$(t u_ssl)" \
        "" "domain | ip | cloudflare | renew | revoke | list | set" \
        "m-ui version|uninstall" "$(t u_misc)" \
        "m-ui --lang <code>" "$(tf u_lang "${MENU_LANGS[*]}")"
}

show_menu() {
    local menu_choice=""
    local i
    echo -e "${blue}$(t menu_title)${plain}"
    for i in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 0; do
        printf '  %2d. %s\n' "$i" "$(t "m_${i}")"
    done
    read -rp "$(t p_select)" menu_choice
    case "$menu_choice" in
        1) start;; 2) stop;; 3) restart;; 4) status;; 5) logs;; 6) settings;;
        7) configure;; 8) reset_credentials;; 9) reset_path;; 10) enable;; 11) disable;;
        12) update;; 13) uninstall;; 14) ssl;; 15) clear_logs;; 0) exit 0;; *) warn "$(t invalid_choice)";;
    esac
}

# ---------------------------------------------------------------------------
# 菜单语言。代码和面板 supportedLanguages 一一对应，大小写、下划线和常见别名都认。

normalize_lang() {
    local value="${1,,}"
    value="${value//_/-}"
    case "$value" in
        en|en-*) echo en ;;
        zh|zh-cn|zh-sg|zh-hans|zh-hans-*|cn|chs) echo zh-CN ;;
        zh-tw|zh-hk|zh-mo|zh-hant|zh-hant-*|tw|cht) echo zh-TW ;;
        ja|ja-jp|jp) echo ja ;;
        ru|ru-ru) echo ru ;;
        fa|fa-ir) echo fa ;;
        vi|vi-vn) echo vi ;;
        es|es-*) echo es ;;
        tr|tr-tr) echo tr ;;
        uk|uk-ua|ua) echo uk ;;
        pt|pt-br|br) echo pt-BR ;;
        *) return 1 ;;
    esac
}

lang_name() {
    case "$1" in
        en) echo "English" ;; zh-CN) echo "简体中文" ;; zh-TW) echo "繁體中文" ;;
        ja) echo "日本語" ;; ru) echo "Русский" ;; fa) echo "فارسی" ;;
        vi) echo "Tiếng Việt" ;; es) echo "Español" ;; tr) echo "Türkçe" ;;
        uk) echo "Українська" ;; pt-BR) echo "Português (Brasil)" ;; *) echo "$1" ;;
    esac
}

# load_lang 先铺英文再覆盖，某条漏翻时退回英文而不是显示键名。
load_lang() {
    lang_en
    case "$1" in
        zh-CN) lang_zh_CN ;; zh-TW) lang_zh_TW ;; ja) lang_ja ;; ru) lang_ru ;; fa) lang_fa ;;
        vi) lang_vi ;; es) lang_es ;; tr) lang_tr ;; uk) lang_uk ;; pt-BR) lang_pt_BR ;;
    esac
    MENU_LANG="$1"
}

show_languages() {
    local code marker
    t lang_list; echo
    for code in "${MENU_LANGS[@]}"; do
        marker=" "; [[ "$code" == "$MENU_LANG" ]] && marker="*"
        printf '  %s %-6s %s\n' "$marker" "$code" "$(lang_name "$code")"
    done
}

save_lang() {
    if ( printf '%s\n' "$1" > "$MENU_LANG_FILE" && chmod 0644 "$MENU_LANG_FILE" ) 2>/dev/null; then
        return 0
    fi
    warn "$(t lang_save_fail)"
}

lang_en() {
    M err_prefix "ERROR:"
    M need_root "this command must run as root"
    M dl_installer_fail "could not download the m-ui installer"
    M dl_menu_fail "could not download the latest management script"
    M menu_updated "Management script updated"
    M installer_syntax "downloaded m-ui installer has invalid shell syntax"
    M acme_missing "acme.sh is not installed; issue a certificate first"
    M acme_not_installed "acme.sh is not installed"
    M p_domain "Domain: "
    M domain_empty "domain cannot be empty"
    M p_ipv4 "IPv4 address (blank detects the public IPv4): "
    M cert_files "Installed certificate files:"
    M domain_unmanaged "domain is not managed by acme.sh: %s"
    M p_revoke "Domain to revoke: "
    M p_renew "Domain to force-renew: "
    M p_cert "Certificate path: "
    M p_key "Private key path: "
    M p_cert_domain "Certificate domain (optional): "
    M cert_bad "certificate file is missing or unreadable"
    M key_bad "private key file is missing or unreadable"
    M tls_applied "TLS certificate paths applied"
    M chain_missing "certificate file %s has no intermediate certificate; Mihomo clients may fail to fetch subscriptions"
    M acme_installing "Installing acme.sh for TLS certificate management..."
    M acme_dl_fail "could not download or run the acme.sh installer"
    M acme_install_fail "acme.sh installation failed"
    M acme_installed "acme.sh installed successfully"
    M domain_invalid "invalid domain"
    M cf_opt_token "Cloudflare API Token (recommended)"
    M cf_opt_key "Cloudflare Global API Key + account email"
    M p_choose_12 "Choose [1-2]: "
    M p_cf_token "Cloudflare API Token: "
    M cf_token_empty "API token cannot be empty"
    M p_cf_key "Cloudflare Global API Key: "
    M p_cf_email "Cloudflare account email: "
    M cf_key_empty "API key and email cannot be empty"
    M invalid_choice "invalid selection"
    M cf_issue_fail "Cloudflare DNS certificate issuance failed"
    M acme_no_files "acme.sh did not install the certificate files"
    M acme_upgrade_fail "certificate works, but acme.sh auto-upgrade could not be enabled"
    M tls_unknown "unknown TLS command: %s"
    M tls_title "TLS certificate management:"
    M tls_1 "Issue Let's Encrypt domain certificate (HTTP-01)"
    M tls_2 "Issue Let's Encrypt IPv4 certificate (short-lived)"
    M tls_3 "Issue domain + wildcard certificate with Cloudflare DNS"
    M tls_4 "Force-renew a certificate"
    M tls_5 "Revoke a certificate"
    M tls_6 "Show existing certificates"
    M tls_7 "Apply existing certificate paths to m-ui"
    M back "Back"
    M p_choose_07 "Choose [0-7]: "
    M p_port "Panel port [1-65535]: "
    M port_invalid "panel port must be between 1 and 65535"
    M logs_cleared "m-ui logs cleared"
    M p_keep_port "Panel port (blank keeps current): "
    M p_keep_path "Panel URI path (blank keeps current): "
    M p_keep_user "Username (blank keeps current): "
    M p_keep_pass "Password (blank keeps current): "
    M p_new_user "New username (blank generates 14 characters): "
    M p_new_pass "New password (blank generates 16 characters): "
    M show_user "Username: %s"
    M show_pass "Password: %s"
    M show_path "Panel path: %s"
    M p_uninstall "Uninstall m-ui? [y/N]: "
    M cancelled "Cancelled"
    M p_purge "Also permanently delete %s? [y/N]: "
    M purge_refuse "refusing to purge unexpected install directory"
    M removed_all "m-ui and its data were removed"
    M removed_keep "m-ui was removed; data remains in %s"
    M usage_title "m-ui management commands:"
    M u_menu "Open the interactive management menu"
    M u_service "Control the m-ui service"
    M u_logs "Show or clear logs"
    M u_settings "Show or change panel settings"
    M u_reset "Reset login credentials or the panel path"
    M u_boot "Enable or disable autostart"
    M u_update "Install or update m-ui, or update this script"
    M u_ssl "Manage TLS certificates"
    M u_misc "Show the version or uninstall"
    M u_lang "Set the menu language (%s)"
    M menu_title "m-ui management"
    M m_1 "Start"
    M m_2 "Stop"
    M m_3 "Restart"
    M m_4 "Status"
    M m_5 "Logs"
    M m_6 "Settings"
    M m_7 "Configure panel"
    M m_8 "Reset credentials"
    M m_9 "Reset panel path"
    M m_10 "Enable autostart"
    M m_11 "Disable autostart"
    M m_12 "Update"
    M m_13 "Uninstall"
    M m_14 "TLS certificates"
    M m_15 "Clear logs"
    M m_0 "Exit"
    M p_select "Select: "
    M unknown_cmd "unknown command: %s"
    M lang_set "Menu language: %s"
    M lang_bad "unsupported language: %s"
    M lang_list "Supported languages:"
    M lang_save_fail "could not save the language preference; it applies to this run only"
}

lang_zh_CN() {
    M err_prefix "错误："
    M need_root "此命令需要以 root 身份运行"
    M dl_installer_fail "无法下载 m-ui 安装脚本"
    M dl_menu_fail "无法下载最新的管理脚本"
    M menu_updated "管理脚本已更新"
    M installer_syntax "下载的 m-ui 安装脚本有语法错误"
    M acme_missing "尚未安装 acme.sh，请先申请证书"
    M acme_not_installed "尚未安装 acme.sh"
    M p_domain "域名："
    M domain_empty "域名不能为空"
    M p_ipv4 "IPv4 地址（留空自动检测公网 IPv4）："
    M cert_files "已安装的证书文件："
    M domain_unmanaged "该域名不由 acme.sh 管理：%s"
    M p_revoke "要吊销的域名："
    M p_renew "要强制续期的域名："
    M p_cert "证书路径："
    M p_key "私钥路径："
    M p_cert_domain "证书域名（可选）："
    M cert_bad "证书文件不存在或无法读取"
    M key_bad "私钥文件不存在或无法读取"
    M tls_applied "TLS 证书路径已应用"
    M chain_missing "证书文件 %s 不含中间证书，Mihomo 客户端可能无法拉取订阅"
    M acme_installing "正在安装 acme.sh 用于管理 TLS 证书..."
    M acme_dl_fail "无法下载或运行 acme.sh 安装程序"
    M acme_install_fail "acme.sh 安装失败"
    M acme_installed "acme.sh 安装成功"
    M domain_invalid "域名格式无效"
    M cf_opt_token "Cloudflare API Token（推荐）"
    M cf_opt_key "Cloudflare Global API Key + 账户邮箱"
    M p_choose_12 "请选择 [1-2]："
    M p_cf_token "Cloudflare API Token："
    M cf_token_empty "API Token 不能为空"
    M p_cf_key "Cloudflare Global API Key："
    M p_cf_email "Cloudflare 账户邮箱："
    M cf_key_empty "API Key 和邮箱不能为空"
    M invalid_choice "无效的选择"
    M cf_issue_fail "通过 Cloudflare DNS 申请证书失败"
    M acme_no_files "acme.sh 没有装出证书文件"
    M acme_upgrade_fail "证书可以正常使用，但无法开启 acme.sh 自动升级"
    M tls_unknown "未知的 TLS 命令：%s"
    M tls_title "TLS 证书管理："
    M tls_1 "申请 Let's Encrypt 域名证书（HTTP-01）"
    M tls_2 "申请 Let's Encrypt IPv4 证书（短期有效）"
    M tls_3 "通过 Cloudflare DNS 申请域名 + 泛域名证书"
    M tls_4 "强制续期证书"
    M tls_5 "吊销证书"
    M tls_6 "查看现有证书"
    M tls_7 "把已有证书路径应用到 m-ui"
    M back "返回"
    M p_choose_07 "请选择 [0-7]："
    M p_port "面板端口 [1-65535]："
    M port_invalid "面板端口必须在 1 到 65535 之间"
    M logs_cleared "m-ui 日志已清除"
    M p_keep_port "面板端口（留空保持不变）："
    M p_keep_path "面板 URI 路径（留空保持不变）："
    M p_keep_user "用户名（留空保持不变）："
    M p_keep_pass "密码（留空保持不变）："
    M p_new_user "新用户名（留空自动生成 14 位）："
    M p_new_pass "新密码（留空自动生成 16 位）："
    M show_user "用户名：%s"
    M show_pass "密码：%s"
    M show_path "面板路径：%s"
    M p_uninstall "确定卸载 m-ui？[y/N]："
    M cancelled "已取消"
    M p_purge "同时永久删除 %s？[y/N]："
    M purge_refuse "安装目录不是预期位置，拒绝删除"
    M removed_all "m-ui 及其数据已删除"
    M removed_keep "m-ui 已卸载，数据保留在 %s"
    M usage_title "m-ui 管理命令："
    M u_menu "打开交互式管理菜单"
    M u_service "控制 m-ui 服务"
    M u_logs "查看或清除日志"
    M u_settings "查看或修改面板设置"
    M u_reset "重置登录凭据或面板路径"
    M u_boot "开启或关闭开机自启"
    M u_update "安装、更新 m-ui，或更新本脚本"
    M u_ssl "管理 TLS 证书"
    M u_misc "查看版本或卸载"
    M u_lang "设置菜单语言（%s）"
    M menu_title "m-ui 管理菜单"
    M m_1 "启动"
    M m_2 "停止"
    M m_3 "重启"
    M m_4 "查看状态"
    M m_5 "查看日志"
    M m_6 "查看设置"
    M m_7 "修改面板设置"
    M m_8 "重置用户名和密码"
    M m_9 "重置面板路径"
    M m_10 "开启开机自启"
    M m_11 "关闭开机自启"
    M m_12 "更新"
    M m_13 "卸载"
    M m_14 "TLS 证书管理"
    M m_15 "清除日志"
    M m_0 "退出"
    M p_select "请选择："
    M unknown_cmd "未知命令：%s"
    M lang_set "菜单语言：%s"
    M lang_bad "不支持的语言：%s"
    M lang_list "支持的语言："
    M lang_save_fail "无法保存语言设置，仅本次生效"
}

lang_zh_TW() {
    M err_prefix "錯誤："
    M need_root "此指令需要以 root 身分執行"
    M dl_installer_fail "無法下載 m-ui 安裝腳本"
    M dl_menu_fail "無法下載最新的管理腳本"
    M menu_updated "管理腳本已更新"
    M installer_syntax "下載的 m-ui 安裝腳本有語法錯誤"
    M acme_missing "尚未安裝 acme.sh，請先申請憑證"
    M acme_not_installed "尚未安裝 acme.sh"
    M p_domain "網域："
    M domain_empty "網域不能為空"
    M p_ipv4 "IPv4 位址（留空自動偵測公網 IPv4）："
    M cert_files "已安裝的憑證檔案："
    M domain_unmanaged "此網域不由 acme.sh 管理：%s"
    M p_revoke "要撤銷的網域："
    M p_renew "要強制續期的網域："
    M p_cert "憑證路徑："
    M p_key "私鑰路徑："
    M p_cert_domain "憑證網域（選填）："
    M cert_bad "憑證檔案不存在或無法讀取"
    M key_bad "私鑰檔案不存在或無法讀取"
    M tls_applied "已套用 TLS 憑證路徑"
    M chain_missing "憑證檔案 %s 不含中繼憑證，Mihomo 用戶端可能無法取得訂閱"
    M acme_installing "正在安裝 acme.sh 以管理 TLS 憑證..."
    M acme_dl_fail "無法下載或執行 acme.sh 安裝程式"
    M acme_install_fail "acme.sh 安裝失敗"
    M acme_installed "acme.sh 安裝成功"
    M domain_invalid "網域格式無效"
    M cf_opt_token "Cloudflare API Token（建議）"
    M cf_opt_key "Cloudflare Global API Key + 帳戶電子郵件"
    M p_choose_12 "請選擇 [1-2]："
    M p_cf_token "Cloudflare API Token："
    M cf_token_empty "API Token 不能為空"
    M p_cf_key "Cloudflare Global API Key："
    M p_cf_email "Cloudflare 帳戶電子郵件："
    M cf_key_empty "API Key 和電子郵件不能為空"
    M invalid_choice "無效的選擇"
    M cf_issue_fail "透過 Cloudflare DNS 申請憑證失敗"
    M acme_no_files "acme.sh 沒有安裝出憑證檔案"
    M acme_upgrade_fail "憑證可以正常使用，但無法啟用 acme.sh 自動升級"
    M tls_unknown "未知的 TLS 指令：%s"
    M tls_title "TLS 憑證管理："
    M tls_1 "申請 Let's Encrypt 網域憑證（HTTP-01）"
    M tls_2 "申請 Let's Encrypt IPv4 憑證（短期有效）"
    M tls_3 "透過 Cloudflare DNS 申請網域 + 萬用字元憑證"
    M tls_4 "強制續期憑證"
    M tls_5 "撤銷憑證"
    M tls_6 "檢視現有憑證"
    M tls_7 "將現有憑證路徑套用到 m-ui"
    M back "返回"
    M p_choose_07 "請選擇 [0-7]："
    M p_port "面板連接埠 [1-65535]："
    M port_invalid "面板連接埠必須介於 1 到 65535 之間"
    M logs_cleared "已清除 m-ui 日誌"
    M p_keep_port "面板連接埠（留空維持不變）："
    M p_keep_path "面板 URI 路徑（留空維持不變）："
    M p_keep_user "使用者名稱（留空維持不變）："
    M p_keep_pass "密碼（留空維持不變）："
    M p_new_user "新使用者名稱（留空自動產生 14 位）："
    M p_new_pass "新密碼（留空自動產生 16 位）："
    M show_user "使用者名稱：%s"
    M show_pass "密碼：%s"
    M show_path "面板路徑：%s"
    M p_uninstall "確定解除安裝 m-ui？[y/N]："
    M cancelled "已取消"
    M p_purge "同時永久刪除 %s？[y/N]："
    M purge_refuse "安裝目錄不是預期位置，拒絕刪除"
    M removed_all "已移除 m-ui 及其資料"
    M removed_keep "已移除 m-ui，資料保留在 %s"
    M usage_title "m-ui 管理指令："
    M u_menu "開啟互動式管理選單"
    M u_service "控制 m-ui 服務"
    M u_logs "檢視或清除日誌"
    M u_settings "檢視或修改面板設定"
    M u_reset "重設登入憑據或面板路徑"
    M u_boot "開啟或關閉開機自動啟動"
    M u_update "安裝、更新 m-ui，或更新本腳本"
    M u_ssl "管理 TLS 憑證"
    M u_misc "檢視版本或解除安裝"
    M u_lang "設定選單語言（%s）"
    M menu_title "m-ui 管理選單"
    M m_1 "啟動"
    M m_2 "停止"
    M m_3 "重新啟動"
    M m_4 "檢視狀態"
    M m_5 "檢視日誌"
    M m_6 "檢視設定"
    M m_7 "修改面板設定"
    M m_8 "重設使用者名稱和密碼"
    M m_9 "重設面板路徑"
    M m_10 "開啟開機自動啟動"
    M m_11 "關閉開機自動啟動"
    M m_12 "更新"
    M m_13 "解除安裝"
    M m_14 "TLS 憑證管理"
    M m_15 "清除日誌"
    M m_0 "離開"
    M p_select "請選擇："
    M unknown_cmd "未知指令：%s"
    M lang_set "選單語言：%s"
    M lang_bad "不支援的語言：%s"
    M lang_list "支援的語言："
    M lang_save_fail "無法儲存語言設定，僅本次有效"
}

lang_ja() {
    M err_prefix "エラー:"
    M need_root "このコマンドは root で実行する必要があります"
    M dl_installer_fail "m-ui インストーラーをダウンロードできませんでした"
    M dl_menu_fail "最新の管理スクリプトをダウンロードできませんでした"
    M menu_updated "管理スクリプトを更新しました"
    M installer_syntax "ダウンロードした m-ui インストーラーに構文エラーがあります"
    M acme_missing "acme.sh がインストールされていません。先に証明書を発行してください"
    M acme_not_installed "acme.sh がインストールされていません"
    M p_domain "ドメイン: "
    M domain_empty "ドメインを入力してください"
    M p_ipv4 "IPv4 アドレス（空欄でパブリック IPv4 を自動検出）: "
    M cert_files "インストール済みの証明書ファイル:"
    M domain_unmanaged "このドメインは acme.sh で管理されていません: %s"
    M p_revoke "失効させるドメイン: "
    M p_renew "強制更新するドメイン: "
    M p_cert "証明書のパス: "
    M p_key "秘密鍵のパス: "
    M p_cert_domain "証明書のドメイン（任意）: "
    M cert_bad "証明書ファイルが存在しないか、読み取れません"
    M key_bad "秘密鍵ファイルが存在しないか、読み取れません"
    M tls_applied "TLS 証明書のパスを適用しました"
    M chain_missing "証明書ファイル %s に中間証明書が含まれていません。Mihomo クライアントがサブスクリプションを取得できない可能性があります"
    M acme_installing "TLS 証明書を管理するため acme.sh をインストールしています..."
    M acme_dl_fail "acme.sh インストーラーをダウンロードまたは実行できませんでした"
    M acme_install_fail "acme.sh のインストールに失敗しました"
    M acme_installed "acme.sh をインストールしました"
    M domain_invalid "無効なドメインです"
    M cf_opt_token "Cloudflare API トークン（推奨）"
    M cf_opt_key "Cloudflare グローバル API キー + アカウントのメールアドレス"
    M p_choose_12 "選択 [1-2]: "
    M p_cf_token "Cloudflare API トークン: "
    M cf_token_empty "API トークンを入力してください"
    M p_cf_key "Cloudflare グローバル API キー: "
    M p_cf_email "Cloudflare アカウントのメールアドレス: "
    M cf_key_empty "API キーとメールアドレスを入力してください"
    M invalid_choice "無効な選択です"
    M cf_issue_fail "Cloudflare DNS による証明書の発行に失敗しました"
    M acme_no_files "acme.sh が証明書ファイルをインストールしませんでした"
    M acme_upgrade_fail "証明書は使用できますが、acme.sh の自動アップグレードを有効にできませんでした"
    M tls_unknown "不明な TLS コマンド: %s"
    M tls_title "TLS 証明書の管理:"
    M tls_1 "Let's Encrypt のドメイン証明書を発行（HTTP-01）"
    M tls_2 "Let's Encrypt の IPv4 証明書を発行（短期間有効）"
    M tls_3 "Cloudflare DNS でドメイン + ワイルドカード証明書を発行"
    M tls_4 "証明書を強制更新"
    M tls_5 "証明書を失効"
    M tls_6 "既存の証明書を表示"
    M tls_7 "既存の証明書パスを m-ui に適用"
    M back "戻る"
    M p_choose_07 "選択 [0-7]: "
    M p_port "パネルのポート [1-65535]: "
    M port_invalid "パネルのポートは 1〜65535 の範囲で指定してください"
    M logs_cleared "m-ui のログを消去しました"
    M p_keep_port "パネルのポート（空欄で変更なし）: "
    M p_keep_path "パネルの URI パス（空欄で変更なし）: "
    M p_keep_user "ユーザー名（空欄で変更なし）: "
    M p_keep_pass "パスワード（空欄で変更なし）: "
    M p_new_user "新しいユーザー名（空欄で 14 文字を自動生成）: "
    M p_new_pass "新しいパスワード（空欄で 16 文字を自動生成）: "
    M show_user "ユーザー名: %s"
    M show_pass "パスワード: %s"
    M show_path "パネルのパス: %s"
    M p_uninstall "m-ui をアンインストールしますか？ [y/N]: "
    M cancelled "キャンセルしました"
    M p_purge "%s も完全に削除しますか？ [y/N]: "
    M purge_refuse "想定外のインストール先のため、削除を中止しました"
    M removed_all "m-ui とそのデータを削除しました"
    M removed_keep "m-ui を削除しました。データは %s に残っています"
    M usage_title "m-ui 管理コマンド:"
    M u_menu "対話式の管理メニューを開く"
    M u_service "m-ui サービスを操作"
    M u_logs "ログを表示または消去"
    M u_settings "パネル設定を表示または変更"
    M u_reset "ログイン情報またはパネルのパスをリセット"
    M u_boot "自動起動を有効化または無効化"
    M u_update "m-ui のインストール・更新、またはこのスクリプトの更新"
    M u_ssl "TLS 証明書を管理"
    M u_misc "バージョンを表示、またはアンインストール"
    M u_lang "メニューの言語を設定（%s）"
    M menu_title "m-ui 管理メニュー"
    M m_1 "起動"
    M m_2 "停止"
    M m_3 "再起動"
    M m_4 "ステータス"
    M m_5 "ログ"
    M m_6 "設定を表示"
    M m_7 "パネル設定を変更"
    M m_8 "ログイン情報をリセット"
    M m_9 "パネルのパスをリセット"
    M m_10 "自動起動を有効化"
    M m_11 "自動起動を無効化"
    M m_12 "更新"
    M m_13 "アンインストール"
    M m_14 "TLS 証明書"
    M m_15 "ログを消去"
    M m_0 "終了"
    M p_select "選択: "
    M unknown_cmd "不明なコマンド: %s"
    M lang_set "メニューの言語: %s"
    M lang_bad "対応していない言語です: %s"
    M lang_list "対応している言語:"
    M lang_save_fail "言語設定を保存できませんでした。今回の実行にのみ適用されます"
}

lang_ru() {
    M err_prefix "ОШИБКА:"
    M need_root "эту команду нужно запускать от имени root"
    M dl_installer_fail "не удалось загрузить установщик m-ui"
    M dl_menu_fail "не удалось загрузить последнюю версию скрипта управления"
    M menu_updated "Скрипт управления обновлён"
    M installer_syntax "в загруженном установщике m-ui есть синтаксические ошибки"
    M acme_missing "acme.sh не установлен; сначала выпустите сертификат"
    M acme_not_installed "acme.sh не установлен"
    M p_domain "Домен: "
    M domain_empty "домен не может быть пустым"
    M p_ipv4 "IPv4-адрес (пусто — определить публичный IPv4): "
    M cert_files "Установленные файлы сертификатов:"
    M domain_unmanaged "домен не управляется acme.sh: %s"
    M p_revoke "Домен для отзыва: "
    M p_renew "Домен для принудительного продления: "
    M p_cert "Путь к сертификату: "
    M p_key "Путь к закрытому ключу: "
    M p_cert_domain "Домен сертификата (необязательно): "
    M cert_bad "файл сертификата отсутствует или недоступен для чтения"
    M key_bad "файл закрытого ключа отсутствует или недоступен для чтения"
    M tls_applied "Пути к TLS-сертификату применены"
    M chain_missing "в файле сертификата %s нет промежуточного сертификата; клиенты Mihomo могут не получить подписку"
    M acme_installing "Установка acme.sh для управления TLS-сертификатами..."
    M acme_dl_fail "не удалось загрузить или запустить установщик acme.sh"
    M acme_install_fail "не удалось установить acme.sh"
    M acme_installed "acme.sh успешно установлен"
    M domain_invalid "недопустимый домен"
    M cf_opt_token "Cloudflare API Token (рекомендуется)"
    M cf_opt_key "Cloudflare Global API Key + email аккаунта"
    M p_choose_12 "Выберите [1-2]: "
    M p_cf_token "Cloudflare API Token: "
    M cf_token_empty "API Token не может быть пустым"
    M p_cf_key "Cloudflare Global API Key: "
    M p_cf_email "Email аккаунта Cloudflare: "
    M cf_key_empty "API Key и email не могут быть пустыми"
    M invalid_choice "неверный выбор"
    M cf_issue_fail "не удалось выпустить сертификат через Cloudflare DNS"
    M acme_no_files "acme.sh не установил файлы сертификата"
    M acme_upgrade_fail "сертификат работает, но включить автообновление acme.sh не удалось"
    M tls_unknown "неизвестная команда TLS: %s"
    M tls_title "Управление TLS-сертификатами:"
    M tls_1 "Выпустить сертификат Let's Encrypt для домена (HTTP-01)"
    M tls_2 "Выпустить сертификат Let's Encrypt для IPv4 (краткосрочный)"
    M tls_3 "Выпустить сертификат на домен и wildcard через Cloudflare DNS"
    M tls_4 "Принудительно продлить сертификат"
    M tls_5 "Отозвать сертификат"
    M tls_6 "Показать имеющиеся сертификаты"
    M tls_7 "Применить в m-ui пути к имеющемуся сертификату"
    M back "Назад"
    M p_choose_07 "Выберите [0-7]: "
    M p_port "Порт панели [1-65535]: "
    M port_invalid "порт панели должен быть от 1 до 65535"
    M logs_cleared "Журналы m-ui очищены"
    M p_keep_port "Порт панели (пусто — оставить текущий): "
    M p_keep_path "URI-путь панели (пусто — оставить текущий): "
    M p_keep_user "Имя пользователя (пусто — оставить текущее): "
    M p_keep_pass "Пароль (пусто — оставить текущий): "
    M p_new_user "Новое имя пользователя (пусто — сгенерировать 14 символов): "
    M p_new_pass "Новый пароль (пусто — сгенерировать 16 символов): "
    M show_user "Имя пользователя: %s"
    M show_pass "Пароль: %s"
    M show_path "Путь панели: %s"
    M p_uninstall "Удалить m-ui? [y/N]: "
    M cancelled "Отменено"
    M p_purge "Также безвозвратно удалить %s? [y/N]: "
    M purge_refuse "неожиданный каталог установки, удаление отменено"
    M removed_all "m-ui и его данные удалены"
    M removed_keep "m-ui удалён; данные остались в %s"
    M usage_title "Команды управления m-ui:"
    M u_menu "Открыть интерактивное меню управления"
    M u_service "Управлять службой m-ui"
    M u_logs "Показать или очистить журналы"
    M u_settings "Показать или изменить настройки панели"
    M u_reset "Сбросить учётные данные или путь панели"
    M u_boot "Включить или отключить автозапуск"
    M u_update "Установить или обновить m-ui, обновить этот скрипт"
    M u_ssl "Управлять TLS-сертификатами"
    M u_misc "Показать версию или удалить"
    M u_lang "Выбрать язык меню (%s)"
    M menu_title "Управление m-ui"
    M m_1 "Запустить"
    M m_2 "Остановить"
    M m_3 "Перезапустить"
    M m_4 "Состояние"
    M m_5 "Журналы"
    M m_6 "Настройки"
    M m_7 "Изменить настройки панели"
    M m_8 "Сбросить учётные данные"
    M m_9 "Сбросить путь панели"
    M m_10 "Включить автозапуск"
    M m_11 "Отключить автозапуск"
    M m_12 "Обновить"
    M m_13 "Удалить"
    M m_14 "TLS-сертификаты"
    M m_15 "Очистить журналы"
    M m_0 "Выход"
    M p_select "Выберите: "
    M unknown_cmd "неизвестная команда: %s"
    M lang_set "Язык меню: %s"
    M lang_bad "язык не поддерживается: %s"
    M lang_list "Поддерживаемые языки:"
    M lang_save_fail "не удалось сохранить выбор языка; он действует только в этот раз"
}

lang_uk() {
    M err_prefix "ПОМИЛКА:"
    M need_root "цю команду потрібно запускати від імені root"
    M dl_installer_fail "не вдалося завантажити інсталятор m-ui"
    M dl_menu_fail "не вдалося завантажити останню версію скрипта керування"
    M menu_updated "Скрипт керування оновлено"
    M installer_syntax "завантажений інсталятор m-ui містить синтаксичні помилки"
    M acme_missing "acme.sh не встановлено; спочатку випустіть сертифікат"
    M acme_not_installed "acme.sh не встановлено"
    M p_domain "Домен: "
    M domain_empty "домен не може бути порожнім"
    M p_ipv4 "IPv4-адреса (порожньо — визначити публічну IPv4): "
    M cert_files "Встановлені файли сертифікатів:"
    M domain_unmanaged "домен не керується acme.sh: %s"
    M p_revoke "Домен для відкликання: "
    M p_renew "Домен для примусового поновлення: "
    M p_cert "Шлях до сертифіката: "
    M p_key "Шлях до закритого ключа: "
    M p_cert_domain "Домен сертифіката (необов'язково): "
    M cert_bad "файл сертифіката відсутній або недоступний для читання"
    M key_bad "файл закритого ключа відсутній або недоступний для читання"
    M tls_applied "Шляхи до TLS-сертифіката застосовано"
    M chain_missing "у файлі сертифіката %s немає проміжного сертифіката; клієнти Mihomo можуть не отримати підписку"
    M acme_installing "Встановлення acme.sh для керування TLS-сертифікатами..."
    M acme_dl_fail "не вдалося завантажити або запустити інсталятор acme.sh"
    M acme_install_fail "не вдалося встановити acme.sh"
    M acme_installed "acme.sh успішно встановлено"
    M domain_invalid "недійсний домен"
    M cf_opt_token "Cloudflare API Token (рекомендовано)"
    M cf_opt_key "Cloudflare Global API Key + email облікового запису"
    M p_choose_12 "Виберіть [1-2]: "
    M p_cf_token "Cloudflare API Token: "
    M cf_token_empty "API Token не може бути порожнім"
    M p_cf_key "Cloudflare Global API Key: "
    M p_cf_email "Email облікового запису Cloudflare: "
    M cf_key_empty "API Key та email не можуть бути порожніми"
    M invalid_choice "неправильний вибір"
    M cf_issue_fail "не вдалося випустити сертифікат через Cloudflare DNS"
    M acme_no_files "acme.sh не встановив файли сертифіката"
    M acme_upgrade_fail "сертифікат працює, але увімкнути автооновлення acme.sh не вдалося"
    M tls_unknown "невідома команда TLS: %s"
    M tls_title "Керування TLS-сертифікатами:"
    M tls_1 "Випустити сертифікат Let's Encrypt для домену (HTTP-01)"
    M tls_2 "Випустити сертифікат Let's Encrypt для IPv4 (короткостроковий)"
    M tls_3 "Випустити сертифікат на домен і wildcard через Cloudflare DNS"
    M tls_4 "Примусово поновити сертифікат"
    M tls_5 "Відкликати сертифікат"
    M tls_6 "Показати наявні сертифікати"
    M tls_7 "Застосувати в m-ui шляхи до наявного сертифіката"
    M back "Назад"
    M p_choose_07 "Виберіть [0-7]: "
    M p_port "Порт панелі [1-65535]: "
    M port_invalid "порт панелі має бути від 1 до 65535"
    M logs_cleared "Журнали m-ui очищено"
    M p_keep_port "Порт панелі (порожньо — залишити поточний): "
    M p_keep_path "URI-шлях панелі (порожньо — залишити поточний): "
    M p_keep_user "Ім'я користувача (порожньо — залишити поточне): "
    M p_keep_pass "Пароль (порожньо — залишити поточний): "
    M p_new_user "Нове ім'я користувача (порожньо — згенерувати 14 символів): "
    M p_new_pass "Новий пароль (порожньо — згенерувати 16 символів): "
    M show_user "Ім'я користувача: %s"
    M show_pass "Пароль: %s"
    M show_path "Шлях панелі: %s"
    M p_uninstall "Видалити m-ui? [y/N]: "
    M cancelled "Скасовано"
    M p_purge "Також остаточно видалити %s? [y/N]: "
    M purge_refuse "неочікуваний каталог встановлення, видалення скасовано"
    M removed_all "m-ui та його дані видалено"
    M removed_keep "m-ui видалено; дані залишилися в %s"
    M usage_title "Команди керування m-ui:"
    M u_menu "Відкрити інтерактивне меню керування"
    M u_service "Керувати службою m-ui"
    M u_logs "Показати або очистити журнали"
    M u_settings "Показати або змінити налаштування панелі"
    M u_reset "Скинути облікові дані або шлях панелі"
    M u_boot "Увімкнути або вимкнути автозапуск"
    M u_update "Встановити або оновити m-ui, оновити цей скрипт"
    M u_ssl "Керувати TLS-сертифікатами"
    M u_misc "Показати версію або видалити"
    M u_lang "Вибрати мову меню (%s)"
    M menu_title "Керування m-ui"
    M m_1 "Запустити"
    M m_2 "Зупинити"
    M m_3 "Перезапустити"
    M m_4 "Стан"
    M m_5 "Журнали"
    M m_6 "Налаштування"
    M m_7 "Змінити налаштування панелі"
    M m_8 "Скинути облікові дані"
    M m_9 "Скинути шлях панелі"
    M m_10 "Увімкнути автозапуск"
    M m_11 "Вимкнути автозапуск"
    M m_12 "Оновити"
    M m_13 "Видалити"
    M m_14 "TLS-сертифікати"
    M m_15 "Очистити журнали"
    M m_0 "Вихід"
    M p_select "Виберіть: "
    M unknown_cmd "невідома команда: %s"
    M lang_set "Мова меню: %s"
    M lang_bad "мова не підтримується: %s"
    M lang_list "Підтримувані мови:"
    M lang_save_fail "не вдалося зберегти вибір мови; він діє лише цього разу"
}

lang_es() {
    M err_prefix "ERROR:"
    M need_root "este comando debe ejecutarse como root"
    M dl_installer_fail "no se pudo descargar el instalador de m-ui"
    M dl_menu_fail "no se pudo descargar el script de gestión más reciente"
    M menu_updated "Script de gestión actualizado"
    M installer_syntax "el instalador de m-ui descargado tiene errores de sintaxis"
    M acme_missing "acme.sh no está instalado; primero emite un certificado"
    M acme_not_installed "acme.sh no está instalado"
    M p_domain "Dominio: "
    M domain_empty "el dominio no puede estar vacío"
    M p_ipv4 "Dirección IPv4 (vacío detecta la IPv4 pública): "
    M cert_files "Archivos de certificado instalados:"
    M domain_unmanaged "el dominio no está gestionado por acme.sh: %s"
    M p_revoke "Dominio a revocar: "
    M p_renew "Dominio a renovar de forma forzada: "
    M p_cert "Ruta del certificado: "
    M p_key "Ruta de la clave privada: "
    M p_cert_domain "Dominio del certificado (opcional): "
    M cert_bad "el archivo del certificado no existe o no se puede leer"
    M key_bad "el archivo de la clave privada no existe o no se puede leer"
    M tls_applied "Rutas del certificado TLS aplicadas"
    M chain_missing "el archivo de certificado %s no incluye el certificado intermedio; los clientes Mihomo podrían no obtener las suscripciones"
    M acme_installing "Instalando acme.sh para gestionar los certificados TLS..."
    M acme_dl_fail "no se pudo descargar o ejecutar el instalador de acme.sh"
    M acme_install_fail "falló la instalación de acme.sh"
    M acme_installed "acme.sh se instaló correctamente"
    M domain_invalid "dominio no válido"
    M cf_opt_token "Cloudflare API Token (recomendado)"
    M cf_opt_key "Cloudflare Global API Key + correo de la cuenta"
    M p_choose_12 "Elige [1-2]: "
    M p_cf_token "Cloudflare API Token: "
    M cf_token_empty "el API Token no puede estar vacío"
    M p_cf_key "Cloudflare Global API Key: "
    M p_cf_email "Correo de la cuenta de Cloudflare: "
    M cf_key_empty "la API Key y el correo no pueden estar vacíos"
    M invalid_choice "selección no válida"
    M cf_issue_fail "falló la emisión del certificado mediante Cloudflare DNS"
    M acme_no_files "acme.sh no instaló los archivos del certificado"
    M acme_upgrade_fail "el certificado funciona, pero no se pudo activar la actualización automática de acme.sh"
    M tls_unknown "comando TLS desconocido: %s"
    M tls_title "Gestión de certificados TLS:"
    M tls_1 "Emitir certificado Let's Encrypt para un dominio (HTTP-01)"
    M tls_2 "Emitir certificado Let's Encrypt para IPv4 (corta duración)"
    M tls_3 "Emitir certificado de dominio + comodín con Cloudflare DNS"
    M tls_4 "Forzar la renovación de un certificado"
    M tls_5 "Revocar un certificado"
    M tls_6 "Mostrar los certificados existentes"
    M tls_7 "Aplicar a m-ui las rutas de un certificado existente"
    M back "Volver"
    M p_choose_07 "Elige [0-7]: "
    M p_port "Puerto del panel [1-65535]: "
    M port_invalid "el puerto del panel debe estar entre 1 y 65535"
    M logs_cleared "Registros de m-ui borrados"
    M p_keep_port "Puerto del panel (vacío mantiene el actual): "
    M p_keep_path "Ruta URI del panel (vacío mantiene la actual): "
    M p_keep_user "Usuario (vacío mantiene el actual): "
    M p_keep_pass "Contraseña (vacío mantiene la actual): "
    M p_new_user "Nuevo usuario (vacío genera 14 caracteres): "
    M p_new_pass "Nueva contraseña (vacío genera 16 caracteres): "
    M show_user "Usuario: %s"
    M show_pass "Contraseña: %s"
    M show_path "Ruta del panel: %s"
    M p_uninstall "¿Desinstalar m-ui? [y/N]: "
    M cancelled "Cancelado"
    M p_purge "¿Eliminar también %s de forma permanente? [y/N]: "
    M purge_refuse "directorio de instalación inesperado; no se eliminará"
    M removed_all "m-ui y sus datos se eliminaron"
    M removed_keep "m-ui se eliminó; los datos siguen en %s"
    M usage_title "Comandos de gestión de m-ui:"
    M u_menu "Abrir el menú de gestión interactivo"
    M u_service "Controlar el servicio m-ui"
    M u_logs "Ver o borrar los registros"
    M u_settings "Ver o cambiar la configuración del panel"
    M u_reset "Restablecer las credenciales o la ruta del panel"
    M u_boot "Activar o desactivar el inicio automático"
    M u_update "Instalar o actualizar m-ui, o actualizar este script"
    M u_ssl "Gestionar los certificados TLS"
    M u_misc "Ver la versión o desinstalar"
    M u_lang "Elegir el idioma del menú (%s)"
    M menu_title "Gestión de m-ui"
    M m_1 "Iniciar"
    M m_2 "Detener"
    M m_3 "Reiniciar"
    M m_4 "Estado"
    M m_5 "Registros"
    M m_6 "Configuración"
    M m_7 "Configurar el panel"
    M m_8 "Restablecer credenciales"
    M m_9 "Restablecer la ruta del panel"
    M m_10 "Activar inicio automático"
    M m_11 "Desactivar inicio automático"
    M m_12 "Actualizar"
    M m_13 "Desinstalar"
    M m_14 "Certificados TLS"
    M m_15 "Borrar registros"
    M m_0 "Salir"
    M p_select "Selecciona: "
    M unknown_cmd "comando desconocido: %s"
    M lang_set "Idioma del menú: %s"
    M lang_bad "idioma no compatible: %s"
    M lang_list "Idiomas disponibles:"
    M lang_save_fail "no se pudo guardar el idioma; solo se aplica a esta ejecución"
}

lang_pt_BR() {
    M err_prefix "ERRO:"
    M need_root "este comando precisa ser executado como root"
    M dl_installer_fail "não foi possível baixar o instalador do m-ui"
    M dl_menu_fail "não foi possível baixar o script de gerenciamento mais recente"
    M menu_updated "Script de gerenciamento atualizado"
    M installer_syntax "o instalador do m-ui baixado tem erros de sintaxe"
    M acme_missing "o acme.sh não está instalado; emita um certificado primeiro"
    M acme_not_installed "o acme.sh não está instalado"
    M p_domain "Domínio: "
    M domain_empty "o domínio não pode ficar vazio"
    M p_ipv4 "Endereço IPv4 (vazio detecta o IPv4 público): "
    M cert_files "Arquivos de certificado instalados:"
    M domain_unmanaged "o domínio não é gerenciado pelo acme.sh: %s"
    M p_revoke "Domínio a revogar: "
    M p_renew "Domínio para forçar a renovação: "
    M p_cert "Caminho do certificado: "
    M p_key "Caminho da chave privada: "
    M p_cert_domain "Domínio do certificado (opcional): "
    M cert_bad "o arquivo do certificado não existe ou não pode ser lido"
    M key_bad "o arquivo da chave privada não existe ou não pode ser lido"
    M tls_applied "Caminhos do certificado TLS aplicados"
    M chain_missing "o arquivo de certificado %s não contém o certificado intermediário; clientes Mihomo podem não conseguir obter as assinaturas"
    M acme_installing "Instalando o acme.sh para gerenciar certificados TLS..."
    M acme_dl_fail "não foi possível baixar ou executar o instalador do acme.sh"
    M acme_install_fail "falha ao instalar o acme.sh"
    M acme_installed "acme.sh instalado com sucesso"
    M domain_invalid "domínio inválido"
    M cf_opt_token "Cloudflare API Token (recomendado)"
    M cf_opt_key "Cloudflare Global API Key + e-mail da conta"
    M p_choose_12 "Escolha [1-2]: "
    M p_cf_token "Cloudflare API Token: "
    M cf_token_empty "o API Token não pode ficar vazio"
    M p_cf_key "Cloudflare Global API Key: "
    M p_cf_email "E-mail da conta Cloudflare: "
    M cf_key_empty "a API Key e o e-mail não podem ficar vazios"
    M invalid_choice "opção inválida"
    M cf_issue_fail "falha ao emitir o certificado via Cloudflare DNS"
    M acme_no_files "o acme.sh não instalou os arquivos do certificado"
    M acme_upgrade_fail "o certificado funciona, mas não foi possível ativar a atualização automática do acme.sh"
    M tls_unknown "comando TLS desconhecido: %s"
    M tls_title "Gerenciamento de certificados TLS:"
    M tls_1 "Emitir certificado Let's Encrypt para domínio (HTTP-01)"
    M tls_2 "Emitir certificado Let's Encrypt para IPv4 (curta duração)"
    M tls_3 "Emitir certificado de domínio + curinga com Cloudflare DNS"
    M tls_4 "Forçar a renovação de um certificado"
    M tls_5 "Revogar um certificado"
    M tls_6 "Mostrar os certificados existentes"
    M tls_7 "Aplicar ao m-ui os caminhos de um certificado existente"
    M back "Voltar"
    M p_choose_07 "Escolha [0-7]: "
    M p_port "Porta do painel [1-65535]: "
    M port_invalid "a porta do painel deve estar entre 1 e 65535"
    M logs_cleared "Logs do m-ui apagados"
    M p_keep_port "Porta do painel (vazio mantém a atual): "
    M p_keep_path "Caminho URI do painel (vazio mantém o atual): "
    M p_keep_user "Usuário (vazio mantém o atual): "
    M p_keep_pass "Senha (vazio mantém a atual): "
    M p_new_user "Novo usuário (vazio gera 14 caracteres): "
    M p_new_pass "Nova senha (vazio gera 16 caracteres): "
    M show_user "Usuário: %s"
    M show_pass "Senha: %s"
    M show_path "Caminho do painel: %s"
    M p_uninstall "Desinstalar o m-ui? [y/N]: "
    M cancelled "Cancelado"
    M p_purge "Excluir também %s permanentemente? [y/N]: "
    M purge_refuse "diretório de instalação inesperado; exclusão recusada"
    M removed_all "O m-ui e seus dados foram removidos"
    M removed_keep "O m-ui foi removido; os dados continuam em %s"
    M usage_title "Comandos de gerenciamento do m-ui:"
    M u_menu "Abrir o menu de gerenciamento interativo"
    M u_service "Controlar o serviço m-ui"
    M u_logs "Ver ou apagar os logs"
    M u_settings "Ver ou alterar as configurações do painel"
    M u_reset "Redefinir as credenciais ou o caminho do painel"
    M u_boot "Ativar ou desativar a inicialização automática"
    M u_update "Instalar ou atualizar o m-ui, ou atualizar este script"
    M u_ssl "Gerenciar certificados TLS"
    M u_misc "Ver a versão ou desinstalar"
    M u_lang "Definir o idioma do menu (%s)"
    M menu_title "Gerenciamento do m-ui"
    M m_1 "Iniciar"
    M m_2 "Parar"
    M m_3 "Reiniciar"
    M m_4 "Status"
    M m_5 "Logs"
    M m_6 "Configurações"
    M m_7 "Configurar o painel"
    M m_8 "Redefinir credenciais"
    M m_9 "Redefinir o caminho do painel"
    M m_10 "Ativar inicialização automática"
    M m_11 "Desativar inicialização automática"
    M m_12 "Atualizar"
    M m_13 "Desinstalar"
    M m_14 "Certificados TLS"
    M m_15 "Apagar logs"
    M m_0 "Sair"
    M p_select "Selecione: "
    M unknown_cmd "comando desconhecido: %s"
    M lang_set "Idioma do menu: %s"
    M lang_bad "idioma não suportado: %s"
    M lang_list "Idiomas suportados:"
    M lang_save_fail "não foi possível salvar o idioma; ele vale só para esta execução"
}

lang_vi() {
    M err_prefix "LỖI:"
    M need_root "lệnh này phải chạy với quyền root"
    M dl_installer_fail "không tải được trình cài đặt m-ui"
    M dl_menu_fail "không tải được script quản lý mới nhất"
    M menu_updated "Đã cập nhật script quản lý"
    M installer_syntax "trình cài đặt m-ui vừa tải về có lỗi cú pháp"
    M acme_missing "chưa cài acme.sh; hãy cấp chứng chỉ trước"
    M acme_not_installed "chưa cài acme.sh"
    M p_domain "Tên miền: "
    M domain_empty "tên miền không được để trống"
    M p_ipv4 "Địa chỉ IPv4 (để trống sẽ tự phát hiện IPv4 công khai): "
    M cert_files "Các tệp chứng chỉ đã cài:"
    M domain_unmanaged "tên miền không do acme.sh quản lý: %s"
    M p_revoke "Tên miền cần thu hồi: "
    M p_renew "Tên miền cần gia hạn bắt buộc: "
    M p_cert "Đường dẫn chứng chỉ: "
    M p_key "Đường dẫn khóa riêng: "
    M p_cert_domain "Tên miền của chứng chỉ (tùy chọn): "
    M cert_bad "tệp chứng chỉ không tồn tại hoặc không đọc được"
    M key_bad "tệp khóa riêng không tồn tại hoặc không đọc được"
    M tls_applied "Đã áp dụng đường dẫn chứng chỉ TLS"
    M chain_missing "tệp chứng chỉ %s không có chứng chỉ trung gian; client Mihomo có thể không tải được gói đăng ký"
    M acme_installing "Đang cài acme.sh để quản lý chứng chỉ TLS..."
    M acme_dl_fail "không tải hoặc chạy được trình cài đặt acme.sh"
    M acme_install_fail "cài acme.sh thất bại"
    M acme_installed "đã cài acme.sh thành công"
    M domain_invalid "tên miền không hợp lệ"
    M cf_opt_token "Cloudflare API Token (khuyên dùng)"
    M cf_opt_key "Cloudflare Global API Key + email tài khoản"
    M p_choose_12 "Chọn [1-2]: "
    M p_cf_token "Cloudflare API Token: "
    M cf_token_empty "API Token không được để trống"
    M p_cf_key "Cloudflare Global API Key: "
    M p_cf_email "Email tài khoản Cloudflare: "
    M cf_key_empty "API Key và email không được để trống"
    M invalid_choice "lựa chọn không hợp lệ"
    M cf_issue_fail "cấp chứng chỉ qua Cloudflare DNS thất bại"
    M acme_no_files "acme.sh không cài được các tệp chứng chỉ"
    M acme_upgrade_fail "chứng chỉ vẫn dùng được, nhưng không bật được tự động nâng cấp acme.sh"
    M tls_unknown "lệnh TLS không xác định: %s"
    M tls_title "Quản lý chứng chỉ TLS:"
    M tls_1 "Cấp chứng chỉ Let's Encrypt cho tên miền (HTTP-01)"
    M tls_2 "Cấp chứng chỉ Let's Encrypt cho IPv4 (ngắn hạn)"
    M tls_3 "Cấp chứng chỉ tên miền + wildcard qua Cloudflare DNS"
    M tls_4 "Gia hạn bắt buộc một chứng chỉ"
    M tls_5 "Thu hồi một chứng chỉ"
    M tls_6 "Xem các chứng chỉ hiện có"
    M tls_7 "Áp dụng đường dẫn chứng chỉ có sẵn cho m-ui"
    M back "Quay lại"
    M p_choose_07 "Chọn [0-7]: "
    M p_port "Cổng bảng điều khiển [1-65535]: "
    M port_invalid "cổng bảng điều khiển phải nằm trong khoảng 1 đến 65535"
    M logs_cleared "Đã xóa nhật ký m-ui"
    M p_keep_port "Cổng bảng điều khiển (để trống giữ nguyên): "
    M p_keep_path "Đường dẫn URI bảng điều khiển (để trống giữ nguyên): "
    M p_keep_user "Tên người dùng (để trống giữ nguyên): "
    M p_keep_pass "Mật khẩu (để trống giữ nguyên): "
    M p_new_user "Tên người dùng mới (để trống sẽ tạo 14 ký tự): "
    M p_new_pass "Mật khẩu mới (để trống sẽ tạo 16 ký tự): "
    M show_user "Tên người dùng: %s"
    M show_pass "Mật khẩu: %s"
    M show_path "Đường dẫn bảng điều khiển: %s"
    M p_uninstall "Gỡ cài đặt m-ui? [y/N]: "
    M cancelled "Đã hủy"
    M p_purge "Xóa vĩnh viễn cả %s? [y/N]: "
    M purge_refuse "thư mục cài đặt không như dự kiến; từ chối xóa"
    M removed_all "Đã gỡ m-ui cùng dữ liệu"
    M removed_keep "Đã gỡ m-ui; dữ liệu vẫn còn trong %s"
    M usage_title "Lệnh quản lý m-ui:"
    M u_menu "Mở menu quản lý tương tác"
    M u_service "Điều khiển dịch vụ m-ui"
    M u_logs "Xem hoặc xóa nhật ký"
    M u_settings "Xem hoặc thay đổi cài đặt bảng điều khiển"
    M u_reset "Đặt lại thông tin đăng nhập hoặc đường dẫn bảng điều khiển"
    M u_boot "Bật hoặc tắt tự khởi động"
    M u_update "Cài đặt, cập nhật m-ui hoặc cập nhật script này"
    M u_ssl "Quản lý chứng chỉ TLS"
    M u_misc "Xem phiên bản hoặc gỡ cài đặt"
    M u_lang "Chọn ngôn ngữ menu (%s)"
    M menu_title "Quản lý m-ui"
    M m_1 "Khởi động"
    M m_2 "Dừng"
    M m_3 "Khởi động lại"
    M m_4 "Trạng thái"
    M m_5 "Nhật ký"
    M m_6 "Cài đặt"
    M m_7 "Cấu hình bảng điều khiển"
    M m_8 "Đặt lại thông tin đăng nhập"
    M m_9 "Đặt lại đường dẫn bảng điều khiển"
    M m_10 "Bật tự khởi động"
    M m_11 "Tắt tự khởi động"
    M m_12 "Cập nhật"
    M m_13 "Gỡ cài đặt"
    M m_14 "Chứng chỉ TLS"
    M m_15 "Xóa nhật ký"
    M m_0 "Thoát"
    M p_select "Chọn: "
    M unknown_cmd "lệnh không xác định: %s"
    M lang_set "Ngôn ngữ menu: %s"
    M lang_bad "ngôn ngữ không được hỗ trợ: %s"
    M lang_list "Các ngôn ngữ được hỗ trợ:"
    M lang_save_fail "không lưu được lựa chọn ngôn ngữ; chỉ áp dụng cho lần chạy này"
}

lang_tr() {
    M err_prefix "HATA:"
    M need_root "bu komut root olarak çalıştırılmalıdır"
    M dl_installer_fail "m-ui yükleyicisi indirilemedi"
    M dl_menu_fail "en güncel yönetim betiği indirilemedi"
    M menu_updated "Yönetim betiği güncellendi"
    M installer_syntax "indirilen m-ui yükleyicisinde sözdizimi hatası var"
    M acme_missing "acme.sh kurulu değil; önce bir sertifika alın"
    M acme_not_installed "acme.sh kurulu değil"
    M p_domain "Alan adı: "
    M domain_empty "alan adı boş olamaz"
    M p_ipv4 "IPv4 adresi (boş bırakılırsa genel IPv4 algılanır): "
    M cert_files "Kurulu sertifika dosyaları:"
    M domain_unmanaged "alan adı acme.sh tarafından yönetilmiyor: %s"
    M p_revoke "İptal edilecek alan adı: "
    M p_renew "Zorla yenilenecek alan adı: "
    M p_cert "Sertifika yolu: "
    M p_key "Özel anahtar yolu: "
    M p_cert_domain "Sertifika alan adı (isteğe bağlı): "
    M cert_bad "sertifika dosyası yok ya da okunamıyor"
    M key_bad "özel anahtar dosyası yok ya da okunamıyor"
    M tls_applied "TLS sertifika yolları uygulandı"
    M chain_missing "%s sertifika dosyasında ara sertifika yok; Mihomo istemcileri abonelikleri alamayabilir"
    M acme_installing "TLS sertifika yönetimi için acme.sh kuruluyor..."
    M acme_dl_fail "acme.sh yükleyicisi indirilemedi ya da çalıştırılamadı"
    M acme_install_fail "acme.sh kurulumu başarısız oldu"
    M acme_installed "acme.sh başarıyla kuruldu"
    M domain_invalid "geçersiz alan adı"
    M cf_opt_token "Cloudflare API Token (önerilir)"
    M cf_opt_key "Cloudflare Global API Key + hesap e-postası"
    M p_choose_12 "Seçin [1-2]: "
    M p_cf_token "Cloudflare API Token: "
    M cf_token_empty "API Token boş olamaz"
    M p_cf_key "Cloudflare Global API Key: "
    M p_cf_email "Cloudflare hesap e-postası: "
    M cf_key_empty "API Key ve e-posta boş olamaz"
    M invalid_choice "geçersiz seçim"
    M cf_issue_fail "Cloudflare DNS ile sertifika alınamadı"
    M acme_no_files "acme.sh sertifika dosyalarını kurmadı"
    M acme_upgrade_fail "sertifika çalışıyor, ancak acme.sh otomatik güncellemesi etkinleştirilemedi"
    M tls_unknown "bilinmeyen TLS komutu: %s"
    M tls_title "TLS sertifika yönetimi:"
    M tls_1 "Alan adı için Let's Encrypt sertifikası al (HTTP-01)"
    M tls_2 "IPv4 için Let's Encrypt sertifikası al (kısa ömürlü)"
    M tls_3 "Cloudflare DNS ile alan adı + joker sertifika al"
    M tls_4 "Bir sertifikayı zorla yenile"
    M tls_5 "Bir sertifikayı iptal et"
    M tls_6 "Mevcut sertifikaları göster"
    M tls_7 "Mevcut sertifika yollarını m-ui'ye uygula"
    M back "Geri"
    M p_choose_07 "Seçin [0-7]: "
    M p_port "Panel portu [1-65535]: "
    M port_invalid "panel portu 1 ile 65535 arasında olmalıdır"
    M logs_cleared "m-ui günlükleri temizlendi"
    M p_keep_port "Panel portu (boş bırakılırsa değişmez): "
    M p_keep_path "Panel URI yolu (boş bırakılırsa değişmez): "
    M p_keep_user "Kullanıcı adı (boş bırakılırsa değişmez): "
    M p_keep_pass "Parola (boş bırakılırsa değişmez): "
    M p_new_user "Yeni kullanıcı adı (boş bırakılırsa 14 karakter üretilir): "
    M p_new_pass "Yeni parola (boş bırakılırsa 16 karakter üretilir): "
    M show_user "Kullanıcı adı: %s"
    M show_pass "Parola: %s"
    M show_path "Panel yolu: %s"
    M p_uninstall "m-ui kaldırılsın mı? [y/N]: "
    M cancelled "İptal edildi"
    M p_purge "%s de kalıcı olarak silinsin mi? [y/N]: "
    M purge_refuse "beklenmeyen kurulum dizini; silme reddedildi"
    M removed_all "m-ui ve verileri kaldırıldı"
    M removed_keep "m-ui kaldırıldı; veriler %s içinde duruyor"
    M usage_title "m-ui yönetim komutları:"
    M u_menu "Etkileşimli yönetim menüsünü aç"
    M u_service "m-ui hizmetini yönet"
    M u_logs "Günlükleri göster veya temizle"
    M u_settings "Panel ayarlarını göster veya değiştir"
    M u_reset "Giriş bilgilerini veya panel yolunu sıfırla"
    M u_boot "Otomatik başlatmayı aç veya kapat"
    M u_update "m-ui'yi kur veya güncelle ya da bu betiği güncelle"
    M u_ssl "TLS sertifikalarını yönet"
    M u_misc "Sürümü göster veya kaldır"
    M u_lang "Menü dilini ayarla (%s)"
    M menu_title "m-ui yönetimi"
    M m_1 "Başlat"
    M m_2 "Durdur"
    M m_3 "Yeniden başlat"
    M m_4 "Durum"
    M m_5 "Günlükler"
    M m_6 "Ayarlar"
    M m_7 "Paneli yapılandır"
    M m_8 "Giriş bilgilerini sıfırla"
    M m_9 "Panel yolunu sıfırla"
    M m_10 "Otomatik başlatmayı aç"
    M m_11 "Otomatik başlatmayı kapat"
    M m_12 "Güncelle"
    M m_13 "Kaldır"
    M m_14 "TLS sertifikaları"
    M m_15 "Günlükleri temizle"
    M m_0 "Çıkış"
    M p_select "Seçin: "
    M unknown_cmd "bilinmeyen komut: %s"
    M lang_set "Menü dili: %s"
    M lang_bad "desteklenmeyen dil: %s"
    M lang_list "Desteklenen diller:"
    M lang_save_fail "dil tercihi kaydedilemedi; yalnızca bu çalıştırmada geçerli"
}

lang_fa() {
    M err_prefix "خطا:"
    M need_root "این دستور باید با دسترسی root اجرا شود"
    M dl_installer_fail "دانلود نصب‌کنندهٔ m-ui ممکن نشد"
    M dl_menu_fail "دانلود آخرین نسخهٔ اسکریپت مدیریت ممکن نشد"
    M menu_updated "اسکریپت مدیریت به‌روزرسانی شد"
    M installer_syntax "نصب‌کنندهٔ دانلودشدهٔ m-ui خطای نحوی دارد"
    M acme_missing "acme.sh نصب نیست؛ ابتدا یک گواهی صادر کنید"
    M acme_not_installed "acme.sh نصب نیست"
    M p_domain "دامنه: "
    M domain_empty "دامنه نمی‌تواند خالی باشد"
    M p_ipv4 "آدرس IPv4 (خالی بگذارید تا IPv4 عمومی شناسایی شود): "
    M cert_files "فایل‌های گواهی نصب‌شده:"
    M domain_unmanaged "این دامنه توسط acme.sh مدیریت نمی‌شود: %s"
    M p_revoke "دامنه برای ابطال: "
    M p_renew "دامنه برای تمدید اجباری: "
    M p_cert "مسیر گواهی: "
    M p_key "مسیر کلید خصوصی: "
    M p_cert_domain "دامنهٔ گواهی (اختیاری): "
    M cert_bad "فایل گواهی وجود ندارد یا قابل خواندن نیست"
    M key_bad "فایل کلید خصوصی وجود ندارد یا قابل خواندن نیست"
    M tls_applied "مسیرهای گواهی TLS اعمال شد"
    M chain_missing "فایل گواهی %s گواهی میانی ندارد؛ ممکن است کلاینت‌های Mihomo نتوانند اشتراک را دریافت کنند"
    M acme_installing "در حال نصب acme.sh برای مدیریت گواهی‌های TLS..."
    M acme_dl_fail "دانلود یا اجرای نصب‌کنندهٔ acme.sh ممکن نشد"
    M acme_install_fail "نصب acme.sh ناموفق بود"
    M acme_installed "acme.sh با موفقیت نصب شد"
    M domain_invalid "دامنه نامعتبر است"
    M cf_opt_token "Cloudflare API Token (پیشنهادی)"
    M cf_opt_key "Cloudflare Global API Key + ایمیل حساب"
    M p_choose_12 "انتخاب کنید [1-2]: "
    M p_cf_token "Cloudflare API Token: "
    M cf_token_empty "API Token نمی‌تواند خالی باشد"
    M p_cf_key "Cloudflare Global API Key: "
    M p_cf_email "ایمیل حساب Cloudflare: "
    M cf_key_empty "API Key و ایمیل نمی‌توانند خالی باشند"
    M invalid_choice "انتخاب نامعتبر"
    M cf_issue_fail "صدور گواهی از طریق Cloudflare DNS ناموفق بود"
    M acme_no_files "acme.sh فایل‌های گواهی را نصب نکرد"
    M acme_upgrade_fail "گواهی کار می‌کند، اما فعال‌سازی ارتقای خودکار acme.sh ممکن نشد"
    M tls_unknown "دستور TLS ناشناخته: %s"
    M tls_title "مدیریت گواهی TLS:"
    M tls_1 "صدور گواهی Let's Encrypt برای دامنه (HTTP-01)"
    M tls_2 "صدور گواهی Let's Encrypt برای IPv4 (کوتاه‌مدت)"
    M tls_3 "صدور گواهی دامنه + wildcard با Cloudflare DNS"
    M tls_4 "تمدید اجباری گواهی"
    M tls_5 "ابطال گواهی"
    M tls_6 "نمایش گواهی‌های موجود"
    M tls_7 "اعمال مسیرهای گواهی موجود روی m-ui"
    M back "بازگشت"
    M p_choose_07 "انتخاب کنید [0-7]: "
    M p_port "پورت پنل [1-65535]: "
    M port_invalid "پورت پنل باید بین 1 تا 65535 باشد"
    M logs_cleared "لاگ‌های m-ui پاک شد"
    M p_keep_port "پورت پنل (خالی = بدون تغییر): "
    M p_keep_path "مسیر URI پنل (خالی = بدون تغییر): "
    M p_keep_user "نام کاربری (خالی = بدون تغییر): "
    M p_keep_pass "رمز عبور (خالی = بدون تغییر): "
    M p_new_user "نام کاربری جدید (خالی = ساخت خودکار 14 نویسه): "
    M p_new_pass "رمز عبور جدید (خالی = ساخت خودکار 16 نویسه): "
    M show_user "نام کاربری: %s"
    M show_pass "رمز عبور: %s"
    M show_path "مسیر پنل: %s"
    M p_uninstall "m-ui حذف شود؟ [y/N]: "
    M cancelled "لغو شد"
    M p_purge "%s هم برای همیشه حذف شود؟ [y/N]: "
    M purge_refuse "پوشهٔ نصب غیرمنتظره است؛ حذف انجام نمی‌شود"
    M removed_all "m-ui و داده‌هایش حذف شد"
    M removed_keep "m-ui حذف شد؛ داده‌ها در %s باقی مانده است"
    M usage_title "دستورهای مدیریت m-ui:"
    M u_menu "باز کردن منوی مدیریت تعاملی"
    M u_service "کنترل سرویس m-ui"
    M u_logs "نمایش یا پاک کردن لاگ‌ها"
    M u_settings "نمایش یا تغییر تنظیمات پنل"
    M u_reset "بازنشانی اطلاعات ورود یا مسیر پنل"
    M u_boot "فعال یا غیرفعال کردن اجرای خودکار"
    M u_update "نصب یا به‌روزرسانی m-ui، یا به‌روزرسانی این اسکریپت"
    M u_ssl "مدیریت گواهی‌های TLS"
    M u_misc "نمایش نسخه یا حذف"
    M u_lang "تنظیم زبان منو (%s)"
    M menu_title "مدیریت m-ui"
    M m_1 "شروع"
    M m_2 "توقف"
    M m_3 "راه‌اندازی مجدد"
    M m_4 "وضعیت"
    M m_5 "لاگ‌ها"
    M m_6 "تنظیمات"
    M m_7 "پیکربندی پنل"
    M m_8 "بازنشانی اطلاعات ورود"
    M m_9 "بازنشانی مسیر پنل"
    M m_10 "فعال‌سازی اجرای خودکار"
    M m_11 "غیرفعال‌سازی اجرای خودکار"
    M m_12 "به‌روزرسانی"
    M m_13 "حذف"
    M m_14 "گواهی‌های TLS"
    M m_15 "پاک کردن لاگ‌ها"
    M m_0 "خروج"
    M p_select "انتخاب: "
    M unknown_cmd "دستور ناشناخته: %s"
    M lang_set "زبان منو: %s"
    M lang_bad "زبان پشتیبانی نمی‌شود: %s"
    M lang_list "زبان‌های پشتیبانی‌شده:"
    M lang_save_fail "ذخیرهٔ زبان ممکن نشد؛ فقط برای همین اجرا اعمال می‌شود"
}

# ---------------------------------------------------------------------------
# --lang 是菜单脚本自己的全局参数，在分发子命令前从参数里摘掉，不会传给
# 安装脚本或 Go 二进制；放在子命令前后都行。

lang_given=0 lang_arg="" rest=()
while [[ $# -gt 0 ]]; do
    case "$1" in
        --lang) lang_given=1; lang_arg="${2:-}"; if [[ $# -ge 2 ]]; then shift 2; else shift; fi ;;
        --lang=*) lang_given=1; lang_arg="${1#--lang=}"; shift ;;
        *) rest+=("$1"); shift ;;
    esac
done
set -- ${rest[@]+"${rest[@]}"}

saved_lang=""
[[ -r "$MENU_LANG_FILE" ]] && saved_lang="$(head -n 1 "$MENU_LANG_FILE" 2>/dev/null || true)"
load_lang "$(normalize_lang "$saved_lang" || echo en)"

if [[ $lang_given -eq 1 ]]; then
    if [[ -z "$lang_arg" ]]; then show_languages; exit 0; fi
    chosen_lang="$(normalize_lang "$lang_arg")" || { show_languages >&2; die "$(tf lang_bad "$lang_arg")"; }
    load_lang "$chosen_lang"
    save_lang "$chosen_lang"
    info "$(tf lang_set "$(lang_name "$chosen_lang")")"
fi

command="${1:-menu}"; [[ $# -gt 0 ]] && shift || true
case "$command" in
    start) start "$@";; stop) stop "$@";; restart) restart "$@";; status) status "$@";;
    logs|log) logs "$@";; clear-logs) clear_logs "$@";; enable) enable "$@";; disable) disable "$@";;
    settings|check-config) settings "$@";; version) version "$@";;
    install) install_cmd "$@";; update) update "$@";; update-menu) update_menu "$@";;
    ssl|cert) ssl "$@";; configure) configure "$@";; set-port|port) set_port "$@";;
    reset-credentials|reset-user) reset_credentials "$@";; reset-path|reset-web-path) reset_path "$@";;
    uninstall) uninstall "$@";; help|-h|--help) show_usage;; menu) show_menu;;
    *) die "$(tf unknown_cmd "$command")";;
esac
