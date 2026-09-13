#!/usr/bin/env bash
set -Eeuo pipefail
umask 077

REPO="RomanovCaesar/m-ui"
MIHOMO_REPO="MetaCubeX/mihomo"
INSTALL_DIR="/usr/local/m-ui"
DATA_DIR="${INSTALL_DIR}/data"
BACKUP_DIR="${DATA_DIR}/backups"
BIN_PATH="${INSTALL_DIR}/m-ui"
CORE_PATH="${INSTALL_DIR}/mihomo"
SYSTEMD_UNIT="/etc/systemd/system/m-ui.service"
OPENRC_UNIT="/etc/init.d/m-ui"
MANAGER_PATH="/usr/local/bin/m-ui"
RAW_BASE="https://raw.githubusercontent.com/${REPO}/main"

red='\033[0;31m'; green='\033[0;32m'; yellow='\033[0;33m'; blue='\033[0;34m'; plain='\033[0m'
[[ -t 1 ]] || red='' green='' yellow='' blue='' plain=''

PORT=""; PANEL_PATH=""; USERNAME=""; PASSWORD=""; LISTEN_IP="0.0.0.0"
VERSION="latest"; MIHOMO_VERSION="latest"; UPDATE_ONLY=0; UPDATE_MIHOMO=0; SKIP_MIHOMO=0
PORT_SET=0; PATH_SET=0; USERNAME_SET=0; PASSWORD_SET=0; LISTEN_SET=0
SSL_MODE=""; SSL_DOMAIN=""; SSL_IP=""; SSL_CERT=""; SSL_KEY=""; NO_SSL=0; SSL_MENU=0
NO_BACKUP=0; BACKUP_KEEP=3; BACKUP_PATH=""
TEMP_DIR=""

info() { echo -e "${green}[m-ui]${plain} $*"; }
warn() { echo -e "${yellow}[m-ui]${plain} $*"; }
die() { echo -e "${red}[m-ui] ERROR:${plain} $*" >&2; exit 1; }

usage() {
    cat <<'EOF'
Install or update m-ui on Linux.

Usage:
  bash install.sh [options]

Options:
  --port PORT             Panel port (default: random unused five-digit port; explicit values may be 1-65535)
  --path PATH             Panel URI path (default: random 16-character value)
  --username USERNAME     Login username (default: random 14-character value)
  --password PASSWORD     Login password (default: random 16-character value)
  --listen-ip IP          Panel listen IP (default: 0.0.0.0)
  --version TAG           Install a release tag, for example v0.1.0
  --update                Update m-ui and preserve all existing data/settings
  --mihomo-version TAG    Install a specific Mihomo release
  --update-mihomo         Replace an existing Mihomo core
  --skip-mihomo           Do not install Mihomo when it is missing
  --ssl-domain DOMAIN     Issue a Let’s Encrypt certificate for DOMAIN
  --ssl-ip IP             Issue a short-lived Let’s Encrypt certificate for IP
  --cert FILE --key FILE  Configure an existing certificate pair
  --no-ssl                Skip the interactive TLS certificate prompt
  --ssl-menu              Configure TLS on an existing installation only
  --no-backup             Do not snapshot the data directory before updating
  --backup-keep N         Snapshots to retain, 1-50 (default: 3)
  -h, --help              Show this help

Environment overrides:
  MUI_BINARY_PATH         Use a local m-ui binary instead of downloading
  MUI_INSTALL_DIR         Installation directory (default /usr/local/m-ui)
  MUI_GITHUB_PROXY        Optional URL prefix, e.g. https://ghproxy.example/
EOF
}

while [[ $# -gt 0 ]]; do
    case "$1" in
        --port) [[ $# -ge 2 ]] || die "--port requires a value"; PORT="$2"; PORT_SET=1; shift 2 ;;
        --port=*) PORT="${1#*=}"; PORT_SET=1; shift ;;
        --path) [[ $# -ge 2 ]] || die "--path requires a value"; PANEL_PATH="$2"; PATH_SET=1; shift 2 ;;
        --path=*) PANEL_PATH="${1#*=}"; PATH_SET=1; shift ;;
        --username) [[ $# -ge 2 ]] || die "--username requires a value"; USERNAME="$2"; USERNAME_SET=1; shift 2 ;;
        --username=*) USERNAME="${1#*=}"; USERNAME_SET=1; shift ;;
        --password) [[ $# -ge 2 ]] || die "--password requires a value"; PASSWORD="$2"; PASSWORD_SET=1; shift 2 ;;
        --password=*) PASSWORD="${1#*=}"; PASSWORD_SET=1; shift ;;
        --listen-ip) [[ $# -ge 2 ]] || die "--listen-ip requires a value"; LISTEN_IP="$2"; LISTEN_SET=1; shift 2 ;;
        --listen-ip=*) LISTEN_IP="${1#*=}"; LISTEN_SET=1; shift ;;
        --version) [[ $# -ge 2 ]] || die "--version requires a value"; VERSION="$2"; shift 2 ;;
        --version=*) VERSION="${1#*=}"; shift ;;
        --mihomo-version) [[ $# -ge 2 ]] || die "--mihomo-version requires a value"; MIHOMO_VERSION="$2"; shift 2 ;;
        --mihomo-version=*) MIHOMO_VERSION="${1#*=}"; shift ;;
        --update) UPDATE_ONLY=1; shift ;;
        --update-mihomo) UPDATE_MIHOMO=1; shift ;;
        --skip-mihomo) SKIP_MIHOMO=1; shift ;;
        --ssl-domain) [[ $# -ge 2 ]] || die "--ssl-domain requires a value"; SSL_DOMAIN="$2"; SSL_MODE=domain; shift 2 ;;
        --ssl-domain=*) SSL_DOMAIN="${1#*=}"; SSL_MODE=domain; shift ;;
        --ssl-ip) [[ $# -ge 2 ]] || die "--ssl-ip requires a value"; SSL_IP="$2"; SSL_MODE=ip; shift 2 ;;
        --ssl-ip=*) SSL_IP="${1#*=}"; SSL_MODE=ip; shift ;;
        --cert) [[ $# -ge 2 ]] || die "--cert requires a value"; SSL_CERT="$2"; SSL_MODE=custom; shift 2 ;;
        --cert=*) SSL_CERT="${1#*=}"; SSL_MODE=custom; shift ;;
        --key) [[ $# -ge 2 ]] || die "--key requires a value"; SSL_KEY="$2"; SSL_MODE=custom; shift 2 ;;
        --key=*) SSL_KEY="${1#*=}"; SSL_MODE=custom; shift ;;
        --no-ssl) NO_SSL=1; shift ;;
        --ssl-menu) SSL_MENU=1; UPDATE_ONLY=1; shift ;;
        --no-backup) NO_BACKUP=1; shift ;;
        --backup-keep) [[ $# -ge 2 ]] || die "--backup-keep requires a value"; BACKUP_KEEP="$2"; shift 2 ;;
        --backup-keep=*) BACKUP_KEEP="${1#*=}"; shift ;;
        -h|--help) usage; exit 0 ;;
        v[0-9]*|[0-9]*) [[ "$VERSION" == "latest" ]] || die "version specified more than once"; VERSION="$1"; shift ;;
        *) die "unknown option: $1" ;;
    esac
done

if [[ -n "${MUI_INSTALL_DIR:-}" ]]; then
    INSTALL_DIR="${MUI_INSTALL_DIR%/}"
    [[ "$INSTALL_DIR" =~ ^/[A-Za-z0-9._/-]+$ ]] || die "MUI_INSTALL_DIR must be an absolute path without spaces"
    DATA_DIR="${INSTALL_DIR}/data"; BIN_PATH="${INSTALL_DIR}/m-ui"; CORE_PATH="${INSTALL_DIR}/mihomo"
    BACKUP_DIR="${DATA_DIR}/backups"
fi

[[ "$BACKUP_KEEP" =~ ^[0-9]+$ ]] && ((BACKUP_KEEP >= 1 && BACKUP_KEEP <= 50)) || die "--backup-keep must be between 1 and 50"

[[ "$(uname -s)" == "Linux" ]] || die "this installer only supports Linux"
[[ ${EUID} -eq 0 ]] || die "run this installer as root"

detect_arch() {
    case "$(uname -m)" in
        x86_64|amd64) echo amd64 ;;
        aarch64|arm64|armv8*) echo arm64 ;;
        armv7l|armv7*) echo armv7 ;;
        armv6l|armv6*) echo armv6 ;;
        armv5l|armv5*) echo armv5 ;;
        i386|i486|i586|i686|x86) echo 386 ;;
        s390x) echo s390x ;;
        *) die "unsupported CPU architecture: $(uname -m)" ;;
    esac
}
ARCH="$(detect_arch)"

source_os_release() {
    if [[ -r /etc/os-release ]]; then . /etc/os-release
    elif [[ -r /usr/lib/os-release ]]; then . /usr/lib/os-release
    else ID=unknown
    fi
}

install_dependencies() {
    local missing=0
    for command in curl openssl tar gzip socat; do command -v "$command" >/dev/null 2>&1 || missing=1; done
    command -v cron >/dev/null 2>&1 || command -v crond >/dev/null 2>&1 || missing=1
    [[ $missing -eq 0 ]] && return
    source_os_release
    info "Installing required packages..."
    case "${ID:-unknown}" in
        ubuntu|debian|armbian|linuxmint)
            apt-get update -y
            DEBIAN_FRONTEND=noninteractive apt-get install -y ca-certificates curl openssl tar gzip socat cron tzdata
            ;;
        fedora|rhel|centos|almalinux|rocky|ol|amzn)
            local pm=dnf; command -v dnf >/dev/null 2>&1 || pm=yum
            "$pm" install -y ca-certificates curl openssl tar gzip socat cronie tzdata
            ;;
        arch|manjaro|parch)
            pacman -Sy --noconfirm ca-certificates curl openssl tar gzip socat cronie tzdata
            ;;
        alpine)
            apk add --no-cache ca-certificates curl openssl tar gzip socat dcron tzdata
            ;;
        opensuse*|sles)
            zypper --non-interactive install ca-certificates curl openssl tar gzip socat cron timezone
            ;;
        *) die "unsupported distribution; install curl, openssl, tar and gzip, then retry" ;;
    esac
}

random_alnum() {
    local length="$1" value
    while true; do
        value="$(openssl rand -base64 $((length * 3)) | LC_ALL=C tr -dc 'A-Za-z0-9' | cut -c "1-${length}")"
        [[ ${#value} -eq $length && "$value" =~ [A-Z] && "$value" =~ [a-z] && "$value" =~ [0-9] ]] && { printf '%s' "$value"; return; }
    done
}

port_in_use() {
    local port="$1"
    if command -v ss >/dev/null 2>&1; then ss -H -ltn "sport = :${port}" 2>/dev/null | grep -q .; return; fi
    if command -v netstat >/dev/null 2>&1; then netstat -lnt 2>/dev/null | awk '{print $4}' | grep -Eq "(^|:)${port}$"; return; fi
    if command -v lsof >/dev/null 2>&1; then lsof -nP -iTCP:"${port}" -sTCP:LISTEN >/dev/null 2>&1; return; fi
    (echo >/dev/tcp/127.0.0.1/"${port}") >/dev/null 2>&1
}

random_port() {
    local attempt number candidate
    for attempt in $(seq 1 256); do
        number="$(od -An -N4 -tu4 /dev/urandom | tr -d ' ')"
        candidate=$((10000 + number % 55536))
        [[ $candidate -eq 12080 || $candidate -eq 9093 ]] && continue
        port_in_use "$candidate" || { echo "$candidate"; return; }
    done
    die "could not find an unused five-digit port"
}

is_domain() {
    [[ "$1" =~ ^[A-Za-z0-9]([A-Za-z0-9-]*[A-Za-z0-9])?(\.[A-Za-z0-9]([A-Za-z0-9-]*[A-Za-z0-9])?)+$ ]]
}

is_ipv4() {
    [[ "$1" =~ ^([0-9]{1,3}\.){3}[0-9]{1,3}$ ]] || return 1
    local octet
    IFS='.' read -r -a octets <<< "$1"
    for octet in "${octets[@]}"; do ((octet >= 0 && octet <= 255)) || return 1; done
}

install_acme() {
    local acme="/root/.acme.sh/acme.sh"
    [[ -x "$acme" ]] && return 0
    info "Installing acme.sh for TLS certificate management..."
    if ! (cd /root && curl -fsSL --retry 3 --connect-timeout 10 https://get.acme.sh | sh); then
        warn "could not download or run the acme.sh installer"
        return 1
    fi
    [[ -x "$acme" ]] || { warn "acme.sh installation failed"; return 1; }
    info "acme.sh installed successfully"
    "$acme" --set-default-ca --server letsencrypt --force >/dev/null 2>&1 || true
}

certificate_http_port() {
    if ! port_in_use 80; then echo 80; return; fi
    warn "TCP port 80 is already in use. Let’s Encrypt still requires external port 80; use a forwarded local port if applicable." >&2
    local selected
    read -rp "ACME standalone local port [default 8080]: " selected
    selected="${selected:-8080}"
    [[ "$selected" =~ ^[0-9]+$ ]] && ((selected >= 1 && selected <= 65535)) || return 1
    port_in_use "$selected" && return 1
    echo "$selected"
}

stop_panel_service() {
    if command -v systemctl >/dev/null 2>&1; then systemctl stop m-ui >/dev/null 2>&1 || true
    elif command -v rc-service >/dev/null 2>&1; then rc-service m-ui stop >/dev/null 2>&1 || true
    fi
}

restart_panel_service() {
    if command -v systemctl >/dev/null 2>&1; then systemctl restart m-ui >/dev/null 2>&1
    elif command -v rc-service >/dev/null 2>&1; then rc-service m-ui restart >/dev/null 2>&1
    else return 1
    fi
}

configure_panel_tls() {
    local cert key domain
    local -a args
    cert="${1:-}"
    key="${2:-}"
    domain="${3:-}"
    [[ -n "$cert" && -n "$key" ]] || { warn "certificate and private key paths are required"; return 1; }
    args=(configure --data-dir "$DATA_DIR" --cert "$cert" --key "$key" --domain "$domain")
    "$BIN_PATH" "${args[@]}" >/dev/null || return 1
    restart_panel_service || return 1
}

issue_domain_certificate() {
    local domain cert_dir acme_port cert key
    domain="${1:-}"
    cert_dir="/root/cert/${domain}"
    is_domain "$domain" || { warn "invalid domain: ${domain}"; return 1; }
    install_acme || return 1
    acme_port="$(certificate_http_port)" || { warn "no available ACME port"; return 1; }
    cert_dir="$(readlink -f "$cert_dir" 2>/dev/null || printf '%s' "/root/cert/${domain}")"
    mkdir -p "$cert_dir"; chmod 0700 "$cert_dir"
    stop_panel_service
    if ! /root/.acme.sh/acme.sh --issue -d "$domain" --standalone --httpport "$acme_port" --force; then
        restart_panel_service >/dev/null 2>&1 || true
        warn "Let’s Encrypt certificate issuance failed for ${domain}"
        return 1
    fi
    cert="${cert_dir}/fullchain.pem"; key="${cert_dir}/privkey.pem"
    /root/.acme.sh/acme.sh --installcert -d "$domain" --key-file "$key" --fullchain-file "$cert" --reloadcmd "systemctl restart m-ui 2>/dev/null || rc-service m-ui restart 2>/dev/null || true" || true
    [[ -s "$cert" && -s "$key" ]] || { restart_panel_service >/dev/null 2>&1 || true; warn "acme.sh did not create certificate files"; return 1; }
    /root/.acme.sh/acme.sh --upgrade --auto-upgrade >/dev/null 2>&1 || warn "certificate works, but acme.sh auto-upgrade could not be enabled"
    chmod 0644 "$cert"; chmod 0600 "$key"
    if ! configure_panel_tls "$cert" "$key" "$domain"; then
        # ACME requires stopping the panel for the standalone challenge. Always
        # bring it back even when applying the certificate or restarting fails.
        restart_panel_service >/dev/null 2>&1 || true
        warn "certificate was issued, but m-ui TLS configuration failed"
        return 1
    fi
}

issue_ip_certificate() {
    local address cert_dir="/root/cert/ip" acme_port cert key
    address="${1:-}"
    is_ipv4 "$address" || { warn "invalid IPv4 address: ${address}"; return 1; }
    install_acme || return 1
    acme_port="$(certificate_http_port)" || { warn "no available ACME port"; return 1; }
    mkdir -p "$cert_dir"; chmod 0700 "$cert_dir"
    stop_panel_service
    if ! /root/.acme.sh/acme.sh --issue -d "$address" --standalone --httpport "$acme_port" --certificate-profile shortlived --force; then
        restart_panel_service >/dev/null 2>&1 || true
        warn "Let’s Encrypt IP certificate issuance failed for ${address}"
        return 1
    fi
    cert="${cert_dir}/fullchain.pem"; key="${cert_dir}/privkey.pem"
    /root/.acme.sh/acme.sh --installcert -d "$address" --key-file "$key" --fullchain-file "$cert" --reloadcmd "systemctl restart m-ui 2>/dev/null || rc-service m-ui restart 2>/dev/null || true" || true
    [[ -s "$cert" && -s "$key" ]] || { restart_panel_service >/dev/null 2>&1 || true; warn "acme.sh did not create IP certificate files"; return 1; }
    /root/.acme.sh/acme.sh --upgrade --auto-upgrade >/dev/null 2>&1 || warn "certificate works, but acme.sh auto-upgrade could not be enabled"
    chmod 0644 "$cert"; chmod 0600 "$key"
    if ! configure_panel_tls "$cert" "$key" "$address"; then
        restart_panel_service >/dev/null 2>&1 || true
        warn "IP certificate was issued, but m-ui TLS configuration failed"
        return 1
    fi
}

configure_custom_certificate() {
    local cert="$1" key="$2" domain="${3:-}"
    [[ -f "$cert" && -r "$cert" && -s "$cert" ]] || { warn "certificate file is missing or unreadable"; return 1; }
    [[ -f "$key" && -r "$key" && -s "$key" ]] || { warn "private key file is missing or unreadable"; return 1; }
    if [[ -n "$domain" ]]; then
        is_domain "$domain" || { warn "invalid certificate domain"; return 1; }
    fi
    configure_panel_tls "$cert" "$key" "$domain"
}

configure_tls() {
    [[ $NO_SSL -eq 1 ]] && return 0
    if [[ -z "$SSL_MODE" && ! -t 0 ]]; then return 0; fi
    if [[ -z "$SSL_MODE" ]]; then
        echo
        echo "TLS certificate setup:"
        echo "  1. Let's Encrypt domain certificate"
        echo "  2. Let's Encrypt IP certificate (short-lived profile)"
        echo "  3. Existing certificate and private key"
        echo "  4. Skip for now"
        local choice domain address cert key
        read -rp "Choose [1-4, default 4]: " choice
        case "${choice:-4}" in
            1) SSL_MODE=domain; read -rp "Domain: " SSL_DOMAIN ;;
            2) SSL_MODE=ip; read -rp "IPv4 address [blank detects public IPv4]: " SSL_IP;;
            3) SSL_MODE=custom; read -rp "Certificate path: " SSL_CERT; read -rp "Private key path: " SSL_KEY; read -rp "Certificate domain (optional): " SSL_DOMAIN ;;
            *) return 0 ;;
        esac
    fi
    case "$SSL_MODE" in
        domain) issue_domain_certificate "$SSL_DOMAIN" ;;
        ip)
            if [[ -z "$SSL_IP" ]]; then SSL_IP="$(curl -4fsSL --max-time 5 https://api4.ipify.org 2>/dev/null || true)"; fi
            issue_ip_certificate "$SSL_IP" ;;
        custom) configure_custom_certificate "$SSL_CERT" "$SSL_KEY" "$SSL_DOMAIN" ;;
        *) die "unknown TLS mode: $SSL_MODE" ;;
    esac
}

validate_config_value() {
    if [[ $PORT_SET -eq 1 ]]; then
        [[ "$PORT" =~ ^[0-9]+$ ]] && ((PORT >= 1 && PORT <= 65535)) || die "panel port must be between 1 and 65535"
        [[ "$PORT" -ne 12080 && "$PORT" -ne 9093 ]] || die "panel port ${PORT} is reserved by m-ui defaults"
        if [[ ! -f "$DATA_DIR/state.json" ]] && port_in_use "$PORT"; then
            die "port ${PORT} is already in use"
        fi
    fi
    if [[ $PATH_SET -eq 1 ]]; then
        PANEL_PATH="${PANEL_PATH#/}"; PANEL_PATH="${PANEL_PATH%/}"
        if [[ -n "$PANEL_PATH" ]]; then
            [[ "$PANEL_PATH" =~ ^[A-Za-z0-9._~/-]+$ && "$PANEL_PATH" != *"//"* ]] || die "path may contain only URL-safe path segments"
            local segment
            IFS='/' read -r -a path_segments <<< "$PANEL_PATH"
            for segment in "${path_segments[@]}"; do [[ "$segment" != "." && "$segment" != ".." && -n "$segment" ]] || die "path cannot contain empty, . or .. segments"; done
        fi
    fi
    [[ $USERNAME_SET -eq 0 || ( -n "$USERNAME" && "$USERNAME" != *$'\n'* && "$USERNAME" != *$'\r'* ) ]] || die "username cannot be empty or contain a newline"
    [[ $PASSWORD_SET -eq 0 || ( -n "$PASSWORD" && "$PASSWORD" != *$'\n'* && "$PASSWORD" != *$'\r'* ) ]] || die "password cannot be empty or contain a newline"
    [[ "$LISTEN_IP" != *$'\n'* && "$LISTEN_IP" != *$'\r'* ]] || die "listen IP is invalid"
}

github_url() {
    local url="$1"
    if [[ -n "${MUI_GITHUB_PROXY:-}" ]]; then printf '%s%s' "${MUI_GITHUB_PROXY%/}/" "$url"; else printf '%s' "$url"; fi
}

fetch_release_json() {
    local repo="$1" version="$2" output="$3" endpoint
    if [[ "$version" == "latest" ]]; then endpoint="https://api.github.com/repos/${repo}/releases/latest"
    else [[ "$version" == v* ]] || version="v${version}"; endpoint="https://api.github.com/repos/${repo}/releases/tags/${version}"
    fi
    curl -fsSL --retry 3 --connect-timeout 10 "$(github_url "$endpoint")" -o "$output"
}

release_tag_from_json() {
    sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$1" | head -n1
}

asset_urls_from_json() {
    sed -n 's/.*"browser_download_url"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$1"
}

download_mui() {
    local destination="$1"
    if [[ -n "${MUI_BINARY_PATH:-}" ]]; then
        [[ -f "$MUI_BINARY_PATH" ]] || die "MUI_BINARY_PATH does not exist"
        cp "$MUI_BINARY_PATH" "$destination"; chmod 0755 "$destination"
        "$destination" version >/dev/null 2>&1 || die "MUI_BINARY_PATH is not a runnable m-ui binary"
        return
    fi
    local json="${TEMP_DIR}/m-ui-release.json" tag url="" candidate
    fetch_release_json "$REPO" "$VERSION" "$json" || die "could not fetch m-ui release information from GitHub"
    tag="$(release_tag_from_json "$json")"; [[ -n "$tag" ]] || die "GitHub release did not contain a tag"
    local asset="m-ui-linux-${ARCH}.tar.gz"
    while IFS= read -r candidate; do
        local base="${candidate##*/}"
        [[ "$base" == "$asset" ]] && { url="$candidate"; break; }
    done < <(asset_urls_from_json "$json")
    [[ -n "$url" ]] || die "release ${tag} has no m-ui Linux asset for ${ARCH}; expected ${asset}"
    info "Downloading m-ui ${tag} for ${ARCH}..."
    local archive="${TEMP_DIR}/${asset}" unpack="${TEMP_DIR}/m-ui-unpack" source=""
    curl -fL --retry 3 --connect-timeout 10 "$(github_url "$url")" -o "$archive" || die "m-ui download failed"
    mkdir -p "$unpack"
    tar -xzf "$archive" -C "$unpack" || die "could not extract ${asset}"
    source="$(find "$unpack" -type f \( -name m-ui -o -name "m-ui-linux-${ARCH}" \) -print -quit)"
    [[ -n "$source" && -f "$source" ]] || die "m-ui release archive did not contain the expected binary"
    [[ "$source" == "$destination" ]] || cp "$source" "$destination"
    chmod 0755 "$destination"
    local reported_version
    reported_version="$("$destination" version 2>/dev/null)" || die "downloaded m-ui binary could not run on this machine"
    [[ -n "$reported_version" ]] || die "downloaded m-ui binary did not report a version"
}

download_mihomo() {
    local destination="$1" json="${TEMP_DIR}/mihomo-release.json" url="" candidate pattern
    [[ -x "$CORE_PATH" && $UPDATE_MIHOMO -eq 0 ]] && { info "Keeping existing Mihomo core"; return; }
    [[ $SKIP_MIHOMO -eq 1 ]] && { warn "Skipping Mihomo installation"; return; }
    fetch_release_json "$MIHOMO_REPO" "$MIHOMO_VERSION" "$json" || die "could not fetch Mihomo release information"
    case "$ARCH" in
        amd64) pattern='/mihomo-linux-amd64-compatible-v[^/]+\.gz$' ;;
        arm64) pattern='/mihomo-linux-arm64-v[^/]+\.gz$' ;;
        armv7) pattern='/mihomo-linux-armv7-v[^/]+\.gz$' ;;
        armv6) pattern='/mihomo-linux-armv6-v[^/]+\.gz$' ;;
        armv5) pattern='/mihomo-linux-armv5-v[^/]+\.gz$' ;;
        386) pattern='/mihomo-linux-386-v[^/]+\.gz$' ;;
        s390x) pattern='/mihomo-linux-s390x-v[^/]+\.gz$' ;;
    esac
    while IFS= read -r candidate; do [[ "$candidate" =~ $pattern ]] && { url="$candidate"; break; }; done < <(asset_urls_from_json "$json")
    [[ -n "$url" ]] || die "Mihomo has no compatible Linux asset for ${ARCH}; use --skip-mihomo and install it manually"
    info "Downloading Mihomo for ${ARCH}..."
    curl -fL --retry 3 --connect-timeout 10 "$(github_url "$url")" | gzip -dc > "$destination"
    chmod 0755 "$destination"
}

install_repo_file() {
    local name="$1" destination="$2" mode="$3" local_file
    local_file="$(cd "$(dirname "${BASH_SOURCE[0]}")" 2>/dev/null && pwd)/${name}"
    if [[ -f "$local_file" ]]; then install -m "$mode" "$local_file" "$destination"
    else curl -fsSL --retry 3 "$(github_url "${RAW_BASE}/${name}")" -o "$destination"; chmod "$mode" "$destination"
    fi
}

# Files the panel cannot regenerate. Geo databases, cache.db and the logs are
# intentionally excluded: they are large, re-downloadable, and would push an
# ordinary snapshot past 40 MB.
BACKUP_FILES=(state.json config.yaml multi-control.json cross-panel-subscriptions.json)

prune_backups() {
    local keep="$1" existing=() candidate index total
    # Glob order is lexicographic, which is chronological here: every snapshot is
    # stamped YYYYmmdd-HHMMSS. Avoids parsing ls, and avoids `head -n -N`, which
    # busybox (Alpine) does not implement.
    for candidate in "${BACKUP_DIR}"/m-ui-data-*.tar.gz; do
        [[ -f "$candidate" ]] && existing+=("$candidate")
    done
    total=${#existing[@]}
    ((total > keep)) || return 0
    for ((index = 0; index < total - keep; index++)); do
        rm -f -- "${existing[index]}"
    done
}

backup_data_dir() {
    BACKUP_PATH=""
    [[ $NO_BACKUP -eq 1 ]] && { info "Skipping the data snapshot (--no-backup)"; return 0; }
    [[ -f "$DATA_DIR/state.json" ]] || return 0
    local present=() name
    for name in "${BACKUP_FILES[@]}"; do
        [[ -f "$DATA_DIR/$name" ]] && present+=("$name")
    done
    ((${#present[@]})) || return 0
    if ! mkdir -p "$BACKUP_DIR" || ! chmod 0700 "$BACKUP_DIR"; then
        warn "could not create ${BACKUP_DIR}; continuing without a snapshot"
        return 0
    fi
    local stamp archive
    stamp="$(date +%Y%m%d-%H%M%S)"
    archive="${BACKUP_DIR}/m-ui-data-${stamp}.tar.gz"
    # Build under a temporary name so an interrupted run never leaves a
    # truncated archive that later looks like a usable snapshot.
    if ! tar -czf "${archive}.partial" -C "$DATA_DIR" "${present[@]}" 2>/dev/null; then
        rm -f -- "${archive}.partial"
        warn "data snapshot failed; continuing with the update"
        return 0
    fi
    mv -f "${archive}.partial" "$archive"
    chmod 0600 "$archive"
    BACKUP_PATH="$archive"
    info "Data snapshot saved to ${archive}"
    prune_backups "$BACKUP_KEEP"
    return 0
}

install_service() {
    if command -v systemctl >/dev/null 2>&1 && [[ -d /run/systemd/system ]]; then
        install_repo_file deploy/m-ui.service "$SYSTEMD_UNIT" 0644
        if [[ "$INSTALL_DIR" != "/usr/local/m-ui" ]]; then sed -i "s#/usr/local/m-ui#${INSTALL_DIR//\#/\\#}#g" "$SYSTEMD_UNIT"; fi
        systemctl daemon-reload
        systemctl enable m-ui >/dev/null
        systemctl restart m-ui
        sleep 1
        systemctl is-active --quiet m-ui || { systemctl status m-ui --no-pager || true; die "m-ui service failed to start"; }
        echo systemd
    elif command -v rc-service >/dev/null 2>&1; then
        install_repo_file deploy/m-ui.openrc "$OPENRC_UNIT" 0755
        if [[ "$INSTALL_DIR" != "/usr/local/m-ui" ]]; then sed -i "s#/usr/local/m-ui#${INSTALL_DIR//\#/\\#}#g" "$OPENRC_UNIT"; fi
        rc-update add m-ui default >/dev/null 2>&1 || true
        rc-service m-ui restart
        echo openrc
    else
        die "neither systemd nor OpenRC was found"
    fi
}

install_dependencies
validate_config_value
TEMP_DIR="$(mktemp -d /tmp/m-ui-install.XXXXXX)"
trap 'rm -rf "$TEMP_DIR"' EXIT
new_install=0; [[ -f "$DATA_DIR/state.json" ]] || new_install=1

if [[ $SSL_MENU -eq 1 ]]; then
    [[ $new_install -eq 0 && -x "$BIN_PATH" ]] || die "m-ui is not installed in ${INSTALL_DIR}"
    configure_tls || die "TLS certificate setup failed"
    exit 0
fi

if [[ $new_install -eq 1 ]]; then
    [[ $UPDATE_ONLY -eq 0 ]] || die "m-ui is not installed; run the installer without --update"
    [[ $PORT_SET -eq 1 ]] || { PORT="$(random_port)"; PORT_SET=1; }
    [[ $PATH_SET -eq 1 ]] || { PANEL_PATH="$(random_alnum 16)"; PATH_SET=1; }
    [[ $USERNAME_SET -eq 1 ]] || { USERNAME="$(random_alnum 14)"; USERNAME_SET=1; }
    [[ $PASSWORD_SET -eq 1 ]] || { PASSWORD="$(random_alnum 16)"; PASSWORD_SET=1; }
    validate_config_value
fi

mkdir -p "$INSTALL_DIR" "$DATA_DIR"
chmod 0700 "$DATA_DIR"

download_mui "${TEMP_DIR}/m-ui"
download_mihomo "${TEMP_DIR}/mihomo"

if command -v systemctl >/dev/null 2>&1; then systemctl stop m-ui >/dev/null 2>&1 || true
elif command -v rc-service >/dev/null 2>&1; then rc-service m-ui stop >/dev/null 2>&1 || true
fi

# Snapshot after the panel is stopped so state.json cannot be rewritten midway,
# and before anything is replaced: the old binaries are restorable, but a state
# file damaged by a bad migration is not.
backup_data_dir

old_binary=""
if [[ -f "$BIN_PATH" ]]; then old_binary="${TEMP_DIR}/m-ui.old"; cp "$BIN_PATH" "$old_binary"; fi
old_core=""
if [[ -f "$CORE_PATH" ]]; then old_core="${TEMP_DIR}/mihomo.old"; cp "$CORE_PATH" "$old_core"; fi
install -m 0755 "${TEMP_DIR}/m-ui" "$BIN_PATH"
if [[ -s "${TEMP_DIR}/mihomo" ]]; then install -m 0755 "${TEMP_DIR}/mihomo" "$CORE_PATH"; fi

configure_args=(configure --data-dir "$DATA_DIR")
if [[ $new_install -eq 1 ]]; then
    configure_args+=(--listen-ip "$LISTEN_IP" --port "$PORT" --path "$PANEL_PATH" --username "$USERNAME" --core-path "$CORE_PATH" --password-stdin)
elif ((PORT_SET || PATH_SET || USERNAME_SET || PASSWORD_SET || LISTEN_SET)); then
    ((PORT_SET)) && configure_args+=(--port "$PORT")
    ((PATH_SET)) && configure_args+=(--path "$PANEL_PATH")
    ((USERNAME_SET)) && configure_args+=(--username "$USERNAME")
    ((LISTEN_SET)) && configure_args+=(--listen-ip "$LISTEN_IP")
    ((PASSWORD_SET)) && configure_args+=(--password-stdin)
fi
if [[ $new_install -eq 1 || ${#configure_args[@]} -gt 3 ]]; then
    if ((PASSWORD_SET)); then printf '%s' "$PASSWORD" | "$BIN_PATH" "${configure_args[@]}" >/dev/null
    else "$BIN_PATH" "${configure_args[@]}" >/dev/null
    fi || {
        [[ -n "$old_binary" ]] && cp "$old_binary" "$BIN_PATH"
        [[ -n "$old_core" ]] && cp "$old_core" "$CORE_PATH"
        restart_panel_service >/dev/null 2>&1 || true
        [[ -n "$BACKUP_PATH" ]] && warn "data snapshot from before this attempt: ${BACKUP_PATH}"
        die "m-ui configuration failed; previous binaries were restored when available"
    }
fi

install_repo_file scripts/m-ui.sh "${INSTALL_DIR}/m-ui.sh" 0755
if [[ "$INSTALL_DIR" != "/usr/local/m-ui" ]]; then sed -i "s#/usr/local/m-ui#${INSTALL_DIR//\#/\\#}#g" "${INSTALL_DIR}/m-ui.sh"; fi
ln -sfn "${INSTALL_DIR}/m-ui.sh" "$MANAGER_PATH"
chown -R root:root "$INSTALL_DIR"
chmod 0700 "$DATA_DIR"
if ! init_system="$(install_service)"; then
    [[ -n "$old_binary" ]] && cp "$old_binary" "$BIN_PATH"
    [[ -n "$old_core" ]] && cp "$old_core" "$CORE_PATH"
    if command -v systemctl >/dev/null 2>&1; then systemctl restart m-ui >/dev/null 2>&1 || true
    elif command -v rc-service >/dev/null 2>&1; then rc-service m-ui restart >/dev/null 2>&1 || true
    fi
    [[ -n "$BACKUP_PATH" ]] && warn "data snapshot from before this attempt: ${BACKUP_PATH}"
    die "service installation failed; the previous binaries were restored when available"
fi

if [[ $new_install -eq 1 || -n "$SSL_MODE" ]]; then
    if ! configure_tls; then
        if [[ -n "$SSL_MODE" ]]; then die "TLS certificate setup failed"; fi
        warn "TLS setup was not completed; configure it later with: m-ui ssl"
    fi
fi

settings="$($BIN_PATH settings --data-dir "$DATA_DIR")"
current_port="$(printf '%s\n' "$settings" | sed -n 's/^port: //p')"
current_path="$(printf '%s\n' "$settings" | sed -n 's/^path: //p')"
current_user="$(printf '%s\n' "$settings" | sed -n 's/^username: //p')"
current_domain="$(printf '%s\n' "$settings" | sed -n 's/^domain: //p')"
current_tls="$(printf '%s\n' "$settings" | sed -n 's/^tls: //p')"
server_ip="$(hostname -I 2>/dev/null | awk '{print $1}')"; server_ip="${server_ip:-SERVER_IP}"
access_scheme=http; access_host="$server_ip"
if [[ "$current_tls" == enabled ]]; then
    access_scheme=https
    [[ -n "$current_domain" ]] && access_host="$current_domain"
fi

echo
info "m-ui installation completed (${init_system})"
echo -e "${green}Access URL:${plain} ${access_scheme}://${access_host}:${current_port}${current_path}"
echo -e "${green}Username:${plain}   ${current_user}"
if [[ $new_install -eq 1 || $PASSWORD_SET -eq 1 ]]; then echo -e "${green}Password:${plain}   ${PASSWORD}"; fi
echo -e "${yellow}Save these credentials now. Configure HTTPS in Panel Settings before exposing the panel publicly.${plain}"
if [[ -n "$BACKUP_PATH" ]]; then
    echo
    echo -e "${green}Data snapshot:${plain} ${BACKUP_PATH}"
    echo "Restore it with:"
    echo "  m-ui stop && tar -xzf ${BACKUP_PATH} -C ${DATA_DIR} && m-ui start"
fi
echo
echo "Management commands:"
echo "  m-ui start | stop | restart | status | logs | settings | configure"
echo "  m-ui update | update-menu | ssl | reset-credentials | reset-path | uninstall"
