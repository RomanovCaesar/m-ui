#!/usr/bin/env bash
set -Eeuo pipefail
umask 077

INSTALL_DIR="${MUI_INSTALL_DIR:-/usr/local/m-ui}"
BIN="${INSTALL_DIR}/m-ui"
DATA_DIR="${INSTALL_DIR}/data"
INSTALL_URL="https://raw.githubusercontent.com/RomanovCaesar/m-ui/main/install.sh"
red='\033[0;31m'; green='\033[0;32m'; yellow='\033[0;33m'; blue='\033[0;34m'; plain='\033[0m'
[[ -t 1 ]] || red='' green='' yellow='' blue='' plain=''

info() { echo -e "${green}[m-ui]${plain} $*"; }
warn() { echo -e "${yellow}[m-ui]${plain} $*"; }
die() { echo -e "${red}[m-ui] ERROR:${plain} $*" >&2; exit 1; }
require_root() { [[ ${EUID} -eq 0 ]] || die "this command must run as root"; }

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
        die "could not download the m-ui installer"
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
        die "could not download the m-ui installer"
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
        die "could not download the latest management script"
    fi
    install -m 0755 "$menu" "${INSTALL_DIR}/m-ui.sh"
    ln -sfn "${INSTALL_DIR}/m-ui.sh" /usr/local/bin/m-ui
    rm -f -- "$menu"
    info "Management script updated"
}

run_ssl_installer() {
    require_root
    local script; script="$(mktemp /tmp/m-ui-ssl.XXXXXX.sh)"
    if ! curl -fsSL --retry 3 "$INSTALL_URL" -o "$script"; then
        rm -f -- "$script"
        die "could not download the m-ui installer"
    fi
    if ! bash -n "$script"; then
        rm -f -- "$script"
        warn "downloaded m-ui installer has invalid shell syntax"
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
    [[ -x "$acme" ]] || die "acme.sh is not installed; issue a certificate first"
    printf '%s' "$acme"
}

ssl_issue_domain() {
    local domain="${1:-}"
    [[ -n "$domain" ]] || read -rp "Domain: " domain
    [[ -n "$domain" ]] || die "domain cannot be empty"
    run_ssl_installer --ssl-menu --ssl-domain "$domain"
}

ssl_issue_ip() {
    local address="${1:-}"
    if [[ -z "$address" ]]; then
        read -rp "IPv4 address (blank detects the public IPv4): " address
    fi
    run_ssl_installer --ssl-menu --ssl-ip "$address"
}

ssl_list() {
    if [[ -x /root/.acme.sh/acme.sh ]]; then
        /root/.acme.sh/acme.sh --list
    else
        warn "acme.sh is not installed"
    fi
    if [[ -d /root/cert ]]; then
        echo
        echo "Installed certificate files:"
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
        warn "domain is not managed by acme.sh: ${domain}"
        return 1
    }
    printf '%s' "$domain"
}

ssl_revoke() {
    local acme domain
    acme="$(acme_binary)"
    domain="$(ssl_select_domain 'Domain to revoke: ')" || return 1
    "$acme" --revoke -d "$domain"
}

ssl_renew() {
    local acme domain
    acme="$(acme_binary)"
    domain="$(ssl_select_domain 'Domain to force-renew: ')" || return 1
    "$acme" --renew -d "$domain" --force
    restart
}

ssl_set_paths() {
    local cert="${1:-}" key="${2:-}" domain="${3:-}"
    [[ -n "$cert" ]] || read -rp "Certificate path: " cert
    [[ -n "$key" ]] || read -rp "Private key path: " key
    if [[ $# -lt 3 ]]; then read -rp "Certificate domain (optional): " domain; fi
    [[ -s "$cert" && -r "$cert" ]] || die "certificate file is missing or unreadable"
    [[ -s "$key" && -r "$key" ]] || die "private key file is missing or unreadable"
    local args=(configure --data-dir "$DATA_DIR" --cert "$cert" --key "$key" --domain "$domain")
    "$BIN" "${args[@]}" >/dev/null
    restart
    info "TLS certificate paths applied"
}

install_acme_for_dns() {
    [[ -x /root/.acme.sh/acme.sh ]] && return 0
    local installer; installer="$(mktemp /tmp/m-ui-acme.XXXXXX.sh)"
    if ! curl -fsSL --retry 3 --connect-timeout 10 https://get.acme.sh -o "$installer"; then
        rm -f -- "$installer"
        return 1
    fi
    sh "$installer" email="" >/dev/null 2>&1 || true
    rm -f -- "$installer"
    [[ -x /root/.acme.sh/acme.sh ]]
}

ssl_cloudflare() {
    require_root
    install_acme_for_dns || die "acme.sh installation failed"
    local domain method secret email="" cert_dir cert key acme="/root/.acme.sh/acme.sh"
    read -rp "Domain: " domain
    [[ "$domain" =~ ^[A-Za-z0-9]([A-Za-z0-9-]*[A-Za-z0-9])?(\.[A-Za-z0-9]([A-Za-z0-9-]*[A-Za-z0-9])?)+$ ]] || die "invalid domain"
    echo "  1. Cloudflare API Token (recommended)"
    echo "  2. Cloudflare Global API Key + account email"
    read -rp "Choose [1-2]: " method
    case "$method" in
        1)
            read -rsp "Cloudflare API Token: " secret; echo
            [[ -n "$secret" ]] || die "API token cannot be empty"
            export CF_Token="$secret"
            ;;
        2)
            read -rsp "Cloudflare Global API Key: " secret; echo
            read -rp "Cloudflare account email: " email
            [[ -n "$secret" && -n "$email" ]] || die "API key and email cannot be empty"
            export CF_Key="$secret" CF_Email="$email"
            ;;
        *) die "invalid selection" ;;
    esac
    "$acme" --set-default-ca --server letsencrypt --force >/dev/null 2>&1 || true
    if ! "$acme" --issue --dns dns_cf -d "$domain" -d "*.${domain}" --force; then
        unset CF_Token CF_Key CF_Email
        die "Cloudflare DNS certificate issuance failed"
    fi
    unset CF_Token CF_Key CF_Email
    cert_dir="/root/cert/${domain}"; cert="${cert_dir}/fullchain.pem"; key="${cert_dir}/privkey.pem"
    mkdir -p "$cert_dir"; chmod 0700 "$cert_dir"
    "$acme" --installcert -d "$domain" --key-file "$key" --fullchain-file "$cert" --reloadcmd "systemctl restart m-ui 2>/dev/null || rc-service m-ui restart 2>/dev/null || true" || true
    [[ -s "$cert" && -s "$key" ]] || die "acme.sh did not install the certificate files"
    chmod 0644 "$cert"; chmod 0600 "$key"
    "$acme" --upgrade --auto-upgrade >/dev/null 2>&1 || warn "certificate works, but acme.sh auto-upgrade could not be enabled"
    ssl_set_paths "$cert" "$key" "$domain"
}

ssl() {
    require_root
    local ssl_command="${1:-menu}" ssl_choice=""
    case "$ssl_command" in
        domain) shift; ssl_issue_domain "$@"; return ;;
        ip) shift; ssl_issue_ip "$@"; return ;;
        cloudflare|cf) shift; ssl_cloudflare "$@"; return ;;
        renew) shift; ssl_renew "$@"; return ;;
        revoke) shift; ssl_revoke "$@"; return ;;
        list) shift; ssl_list "$@"; return ;;
        set) shift; ssl_set_paths "$@"; return ;;
        menu) ;;
        *) die "unknown TLS command: $1" ;;
    esac
    echo "TLS certificate management:"
    echo "  1. Issue Let's Encrypt domain certificate (HTTP-01)"
    echo "  2. Issue Let's Encrypt IPv4 certificate (short-lived)"
    echo "  3. Issue domain + wildcard certificate with Cloudflare DNS"
    echo "  4. Force-renew a certificate"
    echo "  5. Revoke a certificate"
    echo "  6. Show existing certificates"
    echo "  7. Apply existing certificate paths to m-ui"
    echo "  0. Back"
    read -rp "Choose [0-7]: " ssl_choice
    case "$ssl_choice" in
        1) ssl_issue_domain;; 2) ssl_issue_ip;; 3) ssl_cloudflare;; 4) ssl_renew;;
        5) ssl_revoke;; 6) ssl_list;; 7) ssl_set_paths;; 0) return 0;; *) die "invalid selection";;
    esac
}

set_port() {
    require_root
    local port="${1:-}"
    if [[ -z "$port" ]]; then read -rp "Panel port [1-65535]: " port; fi
    [[ "$port" =~ ^[0-9]+$ ]] || die "panel port must be between 1 and 65535"
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
    info "m-ui logs cleared"
}

configure() {
    require_root
    local current port path username password args
    current="$(settings)"
    echo "$current"
    read -rp "Panel port (blank keeps current): " port
    read -rp "Panel URI path (blank keeps current): " path
    read -rp "Username (blank keeps current): " username
    read -rsp "Password (blank keeps current): " password; echo
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
    read -rp "New username (blank generates 14 characters): " username
    [[ -n "$username" ]] || username="$(random_alnum 14)"
    read -rsp "New password (blank generates 16 characters): " password; echo
    [[ -n "$password" ]] || password="$(random_alnum 16)"
    printf '%s' "$password" | "$BIN" configure --data-dir "$DATA_DIR" --username "$username" --password-stdin >/dev/null
    restart
    info "Username: ${username}"
    info "Password: ${password}"
}

reset_path() {
    require_root
    local path; path="$(random_alnum 16)"
    "$BIN" configure --data-dir "$DATA_DIR" --path "$path" >/dev/null
    restart
    info "Panel path: /${path}/"
}

uninstall() {
    require_root
    read -rp "Uninstall m-ui? [y/N]: " answer
    [[ "$answer" =~ ^[Yy]$ ]] || { warn "Cancelled"; return; }
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
    read -rp "Also permanently delete ${DATA_DIR}? [y/N]: " purge
    if [[ "$purge" =~ ^[Yy]$ ]]; then
        [[ "$INSTALL_DIR" == "/usr/local/m-ui" ]] || die "refusing to purge unexpected install directory"
        rm -rf -- "$INSTALL_DIR"
        info "m-ui and its data were removed"
    else
        rm -f -- "$BIN" "${INSTALL_DIR}/mihomo" "${INSTALL_DIR}/m-ui.sh"
        info "m-ui was removed; data remains in ${DATA_DIR}"
    fi
}

show_usage() {
    cat <<'EOF'
m-ui management commands:
  m-ui                         Open the interactive management menu
  m-ui start|stop|restart|status
  m-ui logs|clear-logs
  m-ui settings|configure|set-port
  m-ui reset-credentials|reset-path
  m-ui enable|disable
  m-ui install|update|update-menu
  m-ui ssl [domain|ip|cloudflare|renew|revoke|list|set]
  m-ui version|uninstall
EOF
}

show_menu() {
    local menu_choice=""
    echo -e "${blue}m-ui management${plain}"
    echo "  1. Start                 8. Reset credentials"
    echo "  2. Stop                  9. Reset panel path"
    echo "  3. Restart              10. Enable autostart"
    echo "  4. Status               11. Disable autostart"
    echo "  5. Logs                 12. Update"
    echo "  6. Settings             13. Uninstall"
    echo "  7. Configure panel      14. TLS certificates"
    echo "                         15. Clear logs"
    echo "                          0. Exit"
    read -rp "Select: " menu_choice
    case "$menu_choice" in
        1) start;; 2) stop;; 3) restart;; 4) status;; 5) logs;; 6) settings;;
        7) configure;; 8) reset_credentials;; 9) reset_path;; 10) enable;; 11) disable;;
        12) update;; 13) uninstall;; 14) ssl;; 15) clear_logs;; 0) exit 0;; *) warn "Invalid selection";;
    esac
}

command="${1:-menu}"; [[ $# -gt 0 ]] && shift || true
case "$command" in
    start) start "$@";; stop) stop "$@";; restart) restart "$@";; status) status "$@";;
    logs|log) logs "$@";; clear-logs) clear_logs "$@";; enable) enable "$@";; disable) disable "$@";;
    settings|check-config) settings "$@";; version) version "$@";;
    install) install_cmd "$@";; update) update "$@";; update-menu) update_menu "$@";;
    ssl|cert) ssl "$@";; configure) configure "$@";; set-port|port) set_port "$@";;
    reset-credentials|reset-user) reset_credentials "$@";; reset-path|reset-web-path) reset_path "$@";;
    uninstall) uninstall "$@";; help|-h|--help) show_usage;; menu) show_menu;;
    *) die "unknown command: ${command}";;
esac
