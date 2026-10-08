[English](/README.md) | [简体中文](/README.zh_CN.md) | [繁體中文](/README.zh_TW.md) | [日本語](/README.ja.md) | [Русский](/README.ru.md) | [فارسی](/README.fa.md) | [Tiếng Việt](/README.vi.md) | [Español](/README.es.md) | [Türkçe](/README.tr.md) | [Українська](/README.uk.md) | [Português (Brasil)](/README.pt_BR.md)

<p align="center">
  <img src="./media/m-ui_logo.png" alt="m-ui logo" width="420">
</p>

# m-ui

[![Release](https://img.shields.io/github/v/release/RomanovCaesar/m-ui.svg)](https://github.com/RomanovCaesar/m-ui/releases)
[![Build](https://img.shields.io/github/actions/workflow/status/RomanovCaesar/m-ui/release.yml.svg)](https://github.com/RomanovCaesar/m-ui/actions)
[![Go Version](https://img.shields.io/github/go-mod/go-version/RomanovCaesar/m-ui.svg)](https://github.com/RomanovCaesar/m-ui/blob/main/go.mod)
[![Downloads](https://img.shields.io/github/downloads/RomanovCaesar/m-ui/total.svg)](https://github.com/RomanovCaesar/m-ui/releases/latest)
[![License](https://img.shields.io/github/license/RomanovCaesar/m-ui.svg)](https://github.com/RomanovCaesar/m-ui/blob/main/LICENSE)

Un panel de administración de servidores basado en [Mihomo](https://github.com/MetaCubeX/mihomo). Su flujo de trabajo sigue a 3x-ui, su interfaz sigue el diseño Liquid Glass de Apple y su modelo de configuración es el YAML nativo de Mihomo. El panel está disponible en 11 idiomas.

> [!IMPORTANT]
> Usa este proyecto solo en servidores y redes que te pertenezcan o que tengas autorización para administrar. Respeta la legislación local, las condiciones de tus proveedores y las políticas de los servicios a los que accedes.

La documentación completa está en la [Wiki de m-ui](https://github.com/RomanovCaesar/m-ui/wiki) (en inglés). Sigue nuestro [canal de Telegram](https://t.me/MihomoUI) para conocer las novedades del desarrollo.

## Funciones

**Panel y Mihomo**
* Panel principal con el estado del sistema, el tráfico, las conexiones y Mihomo; Mihomo se inicia automáticamente con el panel
* Generación y validación del YAML nativo de Mihomo, con vista previa y descarga de la configuración
* Cambio de versión de Mihomo desde las versiones oficiales de MetaCubeX y actualización de GeoIP / GeoSite / MetaDB
* Copia de seguridad y restauración completas: ajustes, entradas, identidad de Multi-control (opcional), caché entre paneles y plantillas de suscripción
* Interfaz en inglés, chino simplificado, chino tradicional, japonés, ruso, persa, vietnamita, español, turco, ucraniano y portugués de Brasil

**Entradas y clientes**
* Escuchas `mixed`, `socks`, `http`, `shadowsocks`, `snell`, `vmess`, `vless`, `trojan`, `hysteria2`, `tuic`, `anytls`, `mieru`, `sudoku`, `shadowquic`, `trusttunnel` y `hysteria2-realm`
* Ajustes de TLS, Reality, ShadowTLS, RestTLS, TLSMirror, JLS, Trojan SS, XHTTP, mKCP, Mekya y ofuscación
* Credenciales, cuotas de tráfico, caducidad, reinicios periódicos y estado en línea por cliente; también límites por entrada
* Enlaces para compartir, códigos QR, importación de entradas desde el YAML de un listener de Mihomo y reinicio masivo del tráfico

**Suscripciones**
* Página de suscripción en el navegador con uso, cuota, caducidad y nodos
* **Plantillas de suscripción:** cada suscripción es un perfil completo de cliente, generado a partir de la plantilla integrada (China / Rusia / Irán) o de tu propia plantilla de Mihomo
* Perfiles completos para Mihomo/Clash, Stash, sing-box, Surge, Surfboard, Loon, Quantumult X y Egern; listas de nodos para V2Box, V2RayNG, Shadowrocket y otros clientes de enlaces
* Token de suscripción por usuario y puerto de suscripción dedicado opcional

**Ajustes de Mihomo**
* Basics: modo de enrutamiento, versión de IP para conexiones directas, IPv6, TCP concurrente, estadísticas, registros y enrutamiento básico
* Salidas (incluidas Snell, WireGuard y OpenVPN) y grupos de políticas, con formulario, YAML nativo y conversión de enlaces
* Reglas de enrutamiento evaluadas de arriba abajo
* Salidas de Cloudflare WARP y salidas residenciales de VPNGate por país y proveedor

**Multi-control**
* Red P2P de paneles sin nodo maestro y con identidades firmadas
* Sincronización cifrada de entradas entre paneles de confianza
* Agregación de suscripciones entre paneles

## Instalación en Linux

Ejecuta como `root` en un sistema Linux con systemd u OpenRC:

```bash
bash <(curl -Ls https://raw.githubusercontent.com/RomanovCaesar/m-ui/main/install.sh)
```

El instalador elige un puerto libre de cinco dígitos y genera una ruta de panel de 16 caracteres, un usuario de 14 caracteres y una contraseña de 16 caracteres. Al terminar muestra una sola vez la URL de acceso y las credenciales. También puedes indicarlos tú:

```bash
bash <(curl -Ls https://raw.githubusercontent.com/RomanovCaesar/m-ui/main/install.sh) \
  --port 34567 \
  --path MyPanelA1b2C3d4 \
  --username AdminUserA1234 \
  --password 'ChangeThisA12345'
```

m-ui se instala en `/usr/local/m-ui` y guarda sus datos en `/usr/local/m-ui/data`. El instalador también descarga un núcleo de Mihomo para la arquitectura actual; un núcleo ya instalado se conserva salvo que uses `--update-mihomo`.

`m-ui update` actualiza m-ui conservando todos los ajustes. Antes de actualizar guarda una instantánea del directorio de datos en `data/backups/` y conserva las 3 más recientes.

Cada versión incluye `m-ui-linux-<architecture>.tar.gz` para `386`, `amd64`, `arm64`, `armv5`, `armv6`, `armv7` y `s390x`, además de `m-ui-windows-amd64.zip`.

## Docker

La imagen de Docker es para Linux amd64 e incluye m-ui y un núcleo de Mihomo de versión fija y verificado. Compose usa la red del host, así que los puertos de las entradas que añadas en el panel funcionan sin mapeos de puertos adicionales.

```bash
git clone https://github.com/RomanovCaesar/m-ui.git
cd m-ui
cp .env.example .env
docker compose up -d --build
docker compose logs m-ui
```

Si `MUI_PASSWORD` y `MUI_PATH` están vacíos, el primer arranque los genera y muestra una vez la URL y las credenciales en el registro. Cada versión también publica `ghcr.io/romanovcaesar/m-ui:<tag>` y `latest`; si la imagen está disponible para ti, puedes usar `docker compose pull` en lugar de compilar. Los datos se guardan en los volúmenes `m-ui-data` y `m-ui-core`; no ejecutes `docker compose down -v` a menos que quieras borrarlos.

## Certificados TLS

La primera instalación interactiva ofrece un certificado de Let's Encrypt para un dominio, un certificado de Let's Encrypt de corta duración para IPv4, un certificado propio o seguir sin TLS. Después de instalar, ejecuta:

```bash
m-ui ssl
```

El menú permite emitir certificados para dominios e IP, obtener certificados de dominio y comodín mediante la API DNS de Cloudflare cuando el puerto 80 no está disponible, renovar, revocar, listar y aplicar un certificado existente. Si el archivo del certificado solo contiene el certificado final, m-ui completa la cadena automáticamente, porque clientes como Mihomo Party y FlClash lo rechazan.

## Comandos de gestión

```text
m-ui                  interactive menu
m-ui start | stop | restart | status | logs
m-ui settings | configure | set-port
m-ui reset-credentials | reset-path
m-ui update | update-menu | ssl
m-ui enable | disable | clear-logs | uninstall
m-ui --lang <code>    menu language, e.g. m-ui --lang es
```

El menú habla los mismos 11 idiomas que el panel y recuerda tu elección. El instalador sigue en inglés.

## Documentación

La Wiki está en inglés y en chino simplificado:

* [Installation](https://github.com/RomanovCaesar/m-ui/wiki/Installation) y [Panel Configuration](https://github.com/RomanovCaesar/m-ui/wiki/Configuration)
* [Inbounds and Clients](https://github.com/RomanovCaesar/m-ui/wiki/Inbounds) y [Mihomo Settings](https://github.com/RomanovCaesar/m-ui/wiki/Mihomo-Settings)
* [Subscriptions](https://github.com/RomanovCaesar/m-ui/wiki/Subscriptions) y [Subscription Templates](https://github.com/RomanovCaesar/m-ui/wiki/Subscription-Templates)
* [Multi-control](https://github.com/RomanovCaesar/m-ui/wiki/Multi-control), [Cloudflare WARP](https://github.com/RomanovCaesar/m-ui/wiki/Cloudflare-WARP) y [VPNGate](https://github.com/RomanovCaesar/m-ui/wiki/VPNGate)
* [Maintenance](https://github.com/RomanovCaesar/m-ui/wiki/Maintenance), [API](https://github.com/RomanovCaesar/m-ui/wiki/API) y [Common Questions](https://github.com/RomanovCaesar/m-ui/wiki/Common-questions-and-problems)

## Desarrollo

Ejecución desde el código fuente en Windows; el panel se abre en `http://127.0.0.1:2053` con la cuenta `admin` / `admin`:

```powershell
.\scripts\run.ps1
```

Compila los binarios de Windows y Linux amd64, o todos los archivos de una versión:

```powershell
.\scripts\build.ps1
.\scripts\build-release.ps1
```

Valida con `go test ./...` y `go vet ./...`. La versión se inyecta con `-X main.version=...` desde `MUI_VERSION` o la etiqueta de Git actual. La estructura del código y el flujo de publicación están en [Development and Releases](https://github.com/RomanovCaesar/m-ui/wiki/Development), y la interfaz en [`LIQUID-GLASS.md`](LIQUID-GLASS.md).

## Agradecimientos

* [MetaCubeX/mihomo](https://github.com/MetaCubeX/mihomo): el núcleo proxy y el modelo de configuración
* [MHSanaei/3x-ui](https://github.com/MHSanaei/3x-ui): densidad de la interfaz y flujo de trabajo
* [Sub-Store](https://github.com/sub-store-org/Sub-Store): comportamiento de referencia de los formatos de suscripción
* [liquid-glass-webgl](https://github.com/martin65536/liquid-glass-webgl): los controles Liquid Glass (Apache-2.0, ver [`NOTICE`](NOTICE))

## Licencia

m-ui se distribuye bajo la [GNU General Public License version 3](LICENSE). El código de conversión de enlaces de Mihomo conserva su atribución GPL-3.0 en [`internal/mihomoconvert/NOTICE.md`](internal/mihomoconvert/NOTICE.md).
