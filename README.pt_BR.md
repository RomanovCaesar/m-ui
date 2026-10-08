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

Um painel de gerenciamento de servidores baseado no [Mihomo](https://github.com/MetaCubeX/mihomo). O fluxo de trabalho segue o 3x-ui, a interface segue o design Liquid Glass da Apple e o modelo de configuração é o YAML nativo do Mihomo. O painel está disponível em 11 idiomas.

> [!IMPORTANT]
> Use este projeto apenas em servidores e redes que sejam seus ou que você tenha autorização para administrar. Respeite a legislação local, os termos dos seus provedores e as políticas dos serviços que você acessa.

A documentação completa está na [Wiki do m-ui](https://github.com/RomanovCaesar/m-ui/wiki) (em inglês). Acompanhe as novidades do desenvolvimento no nosso [canal do Telegram](https://t.me/MihomoUI).

## Recursos

**Painel e Mihomo**
* Painel inicial com o estado do sistema, do tráfego, das conexões e do Mihomo; o Mihomo inicia automaticamente com o painel
* Geração e validação do YAML nativo do Mihomo, com visualização e download da configuração
* Troca de versão do Mihomo a partir das versões oficiais da MetaCubeX e atualização de GeoIP / GeoSite / MetaDB
* Backup e restauração completos: configurações, entradas, identidade do Multi-control (opcional), cache entre painéis e modelos de assinatura
* Interface em inglês, chinês simplificado, chinês tradicional, japonês, russo, persa, vietnamita, espanhol, turco, ucraniano e português do Brasil

**Entradas e clientes**
* Listeners `mixed`, `socks`, `http`, `shadowsocks`, `snell`, `vmess`, `vless`, `trojan`, `hysteria2`, `tuic`, `anytls`, `mieru`, `sudoku`, `shadowquic`, `trusttunnel` e `hysteria2-realm`
* Configurações de TLS, Reality, ShadowTLS, RestTLS, TLSMirror, JLS, Trojan SS, XHTTP, mKCP, Mekya e ofuscação
* Credenciais, cotas de tráfego, expiração, redefinições periódicas e status online por cliente; também limites por entrada
* Links de compartilhamento, QR codes, importação de entradas a partir do YAML de um listener do Mihomo e redefinição de tráfego em massa

**Assinaturas**
* Página de assinatura no navegador com uso, cota, expiração e nós
* **Modelos de assinatura:** cada assinatura é um perfil completo de cliente, gerado a partir do modelo embutido (China / Rússia / Irã) ou do seu próprio modelo do Mihomo
* Perfis completos para Mihomo/Clash, Stash, sing-box, Surge, Surfboard, Loon, Quantumult X e Egern; listas de nós para V2Box, V2RayNG, Shadowrocket e outros clientes de links
* Token de assinatura por usuário e porta de assinatura dedicada opcional

**Configurações do Mihomo**
* Basics: modo de roteamento, versão de IP para conexões diretas, IPv6, TCP simultâneo, estatísticas, logs e roteamento básico
* Saídas (incluindo Snell, WireGuard e OpenVPN) e grupos de políticas, com formulário, YAML nativo e conversão de links
* Regras de roteamento avaliadas de cima para baixo
* Saídas do Cloudflare WARP e saídas residenciais do VPNGate por país e provedor

**Multi-control**
* Rede P2P de painéis sem nó mestre, com identidades assinadas
* Sincronização criptografada de entradas entre painéis confiáveis
* Agregação de assinaturas entre painéis

## Instalação no Linux

Execute como `root` em um Linux com systemd ou OpenRC:

```bash
bash <(curl -Ls https://raw.githubusercontent.com/RomanovCaesar/m-ui/main/install.sh)
```

O instalador escolhe uma porta livre de cinco dígitos e gera um caminho de painel de 16 caracteres, um usuário de 14 caracteres e uma senha de 16 caracteres. No final, mostra uma única vez a URL de acesso e as credenciais. Você também pode defini-los:

```bash
bash <(curl -Ls https://raw.githubusercontent.com/RomanovCaesar/m-ui/main/install.sh) \
  --port 34567 \
  --path MyPanelA1b2C3d4 \
  --username AdminUserA1234 \
  --password 'ChangeThisA12345'
```

O m-ui é instalado em `/usr/local/m-ui` e guarda os dados em `/usr/local/m-ui/data`. O instalador também baixa um núcleo do Mihomo para a arquitetura atual; um núcleo já instalado é mantido, a menos que você use `--update-mihomo`.

`m-ui update` atualiza o m-ui mantendo todas as configurações. Antes de atualizar, salva um snapshot do diretório de dados em `data/backups/` e mantém os 3 mais recentes.

Cada versão inclui `m-ui-linux-<architecture>.tar.gz` para `386`, `amd64`, `arm64`, `armv5`, `armv6`, `armv7` e `s390x`, além de `m-ui-windows-amd64.zip`.

## Docker

A imagem Docker é para Linux amd64 e inclui o m-ui e um núcleo do Mihomo de versão fixa e verificado. O Compose usa a rede do host, então as portas das entradas adicionadas no painel funcionam sem mapeamentos extras.

```bash
git clone https://github.com/RomanovCaesar/m-ui.git
cd m-ui
cp .env.example .env
docker compose up -d --build
docker compose logs m-ui
```

Se `MUI_PASSWORD` e `MUI_PATH` estiverem vazios, a primeira inicialização os gera e mostra uma vez a URL e as credenciais no log. Cada versão também publica `ghcr.io/romanovcaesar/m-ui:<tag>` e `latest`; se a imagem estiver disponível para você, use `docker compose pull` em vez de compilar. Os dados ficam nos volumes `m-ui-data` e `m-ui-core`; não execute `docker compose down -v` a menos que queira apagá-los.

## Certificados TLS

A primeira instalação interativa oferece um certificado Let's Encrypt para domínio, um certificado Let's Encrypt de curta duração para IPv4, um certificado existente ou seguir sem TLS. Depois de instalar, execute:

```bash
m-ui ssl
```

O menu permite emitir certificados para domínios e IPs, obter certificados de domínio e curinga pela API DNS da Cloudflare quando a porta 80 não está disponível, renovar, revogar, listar e aplicar um certificado existente. Se o arquivo do certificado tiver apenas o certificado final, o m-ui completa a cadeia automaticamente, porque clientes como Mihomo Party e FlClash o rejeitam.

## Comandos de gerenciamento

```text
m-ui                  interactive menu
m-ui start | stop | restart | status | logs
m-ui settings | configure | set-port
m-ui reset-credentials | reset-path
m-ui update | update-menu | ssl
m-ui enable | disable | clear-logs | uninstall
m-ui --lang <code>    menu language, e.g. m-ui --lang pt-br
```

O menu fala os mesmos 11 idiomas do painel e lembra a sua escolha. O instalador continua em inglês.

## Documentação

A Wiki está em inglês e em chinês simplificado:

* [Installation](https://github.com/RomanovCaesar/m-ui/wiki/Installation) e [Panel Configuration](https://github.com/RomanovCaesar/m-ui/wiki/Configuration)
* [Inbounds and Clients](https://github.com/RomanovCaesar/m-ui/wiki/Inbounds) e [Mihomo Settings](https://github.com/RomanovCaesar/m-ui/wiki/Mihomo-Settings)
* [Subscriptions](https://github.com/RomanovCaesar/m-ui/wiki/Subscriptions) e [Subscription Templates](https://github.com/RomanovCaesar/m-ui/wiki/Subscription-Templates)
* [Multi-control](https://github.com/RomanovCaesar/m-ui/wiki/Multi-control), [Cloudflare WARP](https://github.com/RomanovCaesar/m-ui/wiki/Cloudflare-WARP) e [VPNGate](https://github.com/RomanovCaesar/m-ui/wiki/VPNGate)
* [Maintenance](https://github.com/RomanovCaesar/m-ui/wiki/Maintenance), [API](https://github.com/RomanovCaesar/m-ui/wiki/API) e [Common Questions](https://github.com/RomanovCaesar/m-ui/wiki/Common-questions-and-problems)

## Desenvolvimento

Execução a partir do código-fonte no Windows; o painel abre em `http://127.0.0.1:2053` com a conta `admin` / `admin`:

```powershell
.\scripts\run.ps1
```

Compile os binários de Windows e Linux amd64, ou todos os arquivos de uma versão:

```powershell
.\scripts\build.ps1
.\scripts\build-release.ps1
```

Valide com `go test ./...` e `go vet ./...`. A versão é injetada com `-X main.version=...` a partir de `MUI_VERSION` ou da tag do Git atual. A estrutura do código e o fluxo de publicação estão em [Development and Releases](https://github.com/RomanovCaesar/m-ui/wiki/Development), e a interface em [`LIQUID-GLASS.md`](LIQUID-GLASS.md).

## Agradecimentos

* [MetaCubeX/mihomo](https://github.com/MetaCubeX/mihomo): o núcleo proxy e o modelo de configuração
* [MHSanaei/3x-ui](https://github.com/MHSanaei/3x-ui): densidade da interface e fluxo de trabalho
* [Sub-Store](https://github.com/sub-store-org/Sub-Store): comportamento de referência dos formatos de assinatura
* [liquid-glass-webgl](https://github.com/martin65536/liquid-glass-webgl): os controles Liquid Glass (Apache-2.0, veja [`NOTICE`](NOTICE))

## Licença

O m-ui é distribuído sob a [GNU General Public License version 3](LICENSE). O código de conversão de links do Mihomo mantém sua atribuição GPL-3.0 em [`internal/mihomoconvert/NOTICE.md`](internal/mihomoconvert/NOTICE.md).
