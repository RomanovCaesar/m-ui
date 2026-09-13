#!/bin/sh

set -eu

MUI_BIN=/opt/m-ui/bin/m-ui
DATA_DIR=${MUI_DATA_DIR:-/opt/m-ui/data}
CORE_PATH=${MUI_CORE_PATH:-/opt/m-ui/core/mihomo}
LISTEN_IP=${MUI_LISTEN_IP:-0.0.0.0}
PANEL_PORT=${MUI_PANEL_PORT:-2053}

random_hex() {
    bytes=$1
    od -An -N "$bytes" -tx1 /dev/urandom | tr -d ' \n'
}

mkdir -p "$DATA_DIR" "$(dirname "$CORE_PATH")"

if [ ! -x "$CORE_PATH" ]; then
    cp /opt/m-ui/bootstrap/mihomo "$CORE_PATH"
    chmod 0755 "$CORE_PATH"
    echo "Installed bundled Mihomo core at $CORE_PATH"
fi

# Explicit arguments run the m-ui CLI without changing panel settings. This
# keeps commands such as `docker compose run --rm m-ui version` predictable.
if [ "$#" -gt 0 ]; then
    exec "$MUI_BIN" "$@"
fi

if [ ! -f "$DATA_DIR/state.json" ]; then
    username=${MUI_USERNAME:-admin}
    password=${MUI_PASSWORD:-}
    panel_path=${MUI_PATH:-}

    if [ -z "$password" ]; then
        password=$(random_hex 16)
    fi
    if [ -z "$panel_path" ]; then
        panel_path="/$(random_hex 8)/"
    fi

    set -- configure \
        --data-dir "$DATA_DIR" \
        --core-path "$CORE_PATH" \
        --listen-ip "$LISTEN_IP" \
        --port "$PANEL_PORT" \
        --path "$panel_path" \
        --username "$username"

    printf '%s\n' "$password" | "$MUI_BIN" "$@" --password-stdin

    echo
    echo "m-ui was initialized. Save these values now:"
    echo "  URL:      http://<server-ip>:${PANEL_PORT}${panel_path}"
    echo "  Username: $username"
    echo "  Password: $password"
    echo
else
    # Container networking requires an externally reachable listen address.
    # CorePath is also pinned to the persistent core volume so in-panel Mihomo
    # updates survive image replacement. All other saved settings are retained.
    "$MUI_BIN" configure \
        --data-dir "$DATA_DIR" \
        --core-path "$CORE_PATH" \
        --listen-ip "$LISTEN_IP" >/dev/null
fi

cd /opt/m-ui
exec "$MUI_BIN"
