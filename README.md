# metatube-plus

在 [metatube-community/metatube-sdk-go](https://github.com/metatube-community/metatube-sdk-go) 基础上增强的元数据中文化后端，自带运维控制台；与 Emby 的 MetaTube 插件配套使用，适合个人/家庭的 Emby 媒体库（部署在 NAS、小主机或 Docker 里都可以）。

> A fork of `metatube-sdk-go` that adds metadata Sinicization (Chinese actor and genre normalization), an operator console, JavDB actor fallback and batch library repair, for personal Emby libraries.

[![Go Linter](https://img.shields.io/github/actions/workflow/status/tianhaosay/metatube-sdk-go/linter.yml?branch=main&style=flat-square&logo=githubactions&label=golangci-lint)](https://github.com/tianhaosay/metatube-sdk-go/actions/workflows/linter.yml)
[![Test](https://img.shields.io/github/actions/workflow/status/tianhaosay/metatube-sdk-go/test.yml?branch=main&style=flat-square&logo=github-actions&label=test)](https://github.com/tianhaosay/metatube-sdk-go/actions/workflows/test.yml)
[![Release](https://img.shields.io/github/v/release/tianhaosay/metatube-sdk-go?style=flat-square&label=release)](https://github.com/tianhaosay/metatube-sdk-go/releases)
[![Go Version](https://img.shields.io/badge/go-1.26-30dff3?style=flat-square&logo=go)](go.mod)
[![License](https://img.shields.io/badge/license-Apache--2.0-e4682a?style=flat-square)](LICENSE)

## 与上游的关系

- 这是**功能增强分支**，不是替代品：刮削逻辑、数据源实现、数据库层都沿用上游，新增能力以独立包「外挂」在关键位置挂钩子。
- 规模：新增 34 个文件（约 5.6k 行），修改上游 14 个文件（约 +0.5k 行）。
- 上游仍是权威来源；同步方式见[升级上游](#升级上游)。

## 三个部件，别搞混

| 部件 | 在哪 | 来源 | 负责什么 |
|---|---|---|---|
| MetaTube 插件 | Emby 内（`MetaTube.dll`） | 上游原版 + 本套定制（见下） | 取数通道、标题/简介翻译、插件设置页；定制只多 4 个与后端联动的开关 |
| 后端（本仓库） | 容器 `metatube`，默认 `:8080` | 本 fork | 演员/流派中文化、简介处理、体检、批量修复、日志 |
| 控制台（本仓库） | `http://<host>:8080/ui` | 本 fork | 库体检、数据源与映射同步、日志 |

## 配套插件（推荐组合）

后端搭配定制插件 [`tianhaosay/metatube-plus`](https://github.com/tianhaosay/metatube-plus) 才能完整发挥：

- 插件设置页直接出现后端要读的 4 个开关（隐藏作品简介 / 演员兜底 / FANZA 冷却 / JavDB 冷却），改完约 15 秒生效，不用碰配置文件；
- 默认 `TranslationMode=Both`、`TranslationEngine=GoogleFree`，装上就能翻标题与简介，不需要先填密钥；
- 下载：[插件 Releases](https://github.com/tianhaosay/metatube-plus/releases)（`MetaTube.zip` / `MetaTube.dll`）。

只装上游原版插件也能跑，但那 4 个开关要手工写进 `MetaTube.json`，并且没有控制台、库体检和批量修复。

## 功能

| 能力 | 说明 |
|---|---|
| 中文化管线 | 演员名按映射表归一、流派先查词典再翻（结果写回词典）、简介折叠空行或清空；对所有刮削源生效 |
| 演员映射表 | 从 GitHub 定时同步（约 2.6 万条），文件变动后热加载，无需重启 |
| 演员兜底 | 主源没有演员时向 JavDB 查询一次；失败结果缓存 12 小时，避免全库刷新反复打空 |
| 全库体检 | 6 类问题（无标题 / 标题日文 / 演员日文 / 无演员 / 流派日文 / 无封面）+ 「演员未收录（需人工）」提示 |
| 批量修复 | 重刮、标题修正、清空元数据；分批、可暂停、进度存盘可续跑，双进度条区分「已派发」与「后台已完成」 |
| 无码破解标签 | 文件名带 `-LTX` / `-U` / `-UC` 的影片追加「无码破解」类型，支持预览模式 |
| 运维控制台 | `/ui` 三个页签；基础设置只读镜像，真正修改入口在插件页 |
| 日志检索 | 按番号 / 级别 / 来源（抓取、兜底、翻译、引擎、接口）过滤 |
| JavDB 数据源 | 内置但默认关闭（优先级 0），需要时显式开启 |

## 翻译与配置的分工

**只有一个配置入口：Emby → 插件 → MetaTube 设置页。** 后端通过只读挂载读取同一份 `MetaTube.json`，约 15 秒热加载，不需要重启。

| 内容 | 负责方 | 备注 |
|---|---|---|
| 标题 / 简介 | Emby 插件 | 插件的 `TranslationMode` **必须非 `Disabled`**，否则没人翻 |
| 流派 / 标签 | 后端 | 词典优先，未命中时调用插件配置的引擎，结果写回词典 |
| 演员名 | 后端 | 映射表归一 |
| 简介隐藏 | 后端 | 单纯清空字段，与翻译无关 |

引擎、密钥、翻译模式、隐藏简介、演员兜底、数据源冷却都在插件页；控制台「数据源与映射同步」只做只读展示。

## 下载

- 版本与变更记录：[Releases](https://github.com/tianhaosay/metatube-sdk-go/releases)
- 源码：`git clone` 或下载 Release 的源码包
- 镜像：克隆后 `make image`（需要 Docker），或按下面的 compose 模板自行构建

## 部署

镜像 `metatube-plus:latest`，模板 [`deploy/docker-compose.fork.yml`](deploy/docker-compose.fork.yml)。
模板里的地址、密钥、库 ID 都是占位值，按自己的环境替换：

```bash
cp deploy/docker-compose.fork.yml docker-compose.yml
# 改三处：Emby 地址、Emby API Key、媒体库 ID（可留空表示全部库）
docker compose up -d
```

**最小配置**分两部分，只跑刮削时其实不需要控制台：

```yaml
# 1) 映射表与词典（中文化管线；三者的目录必须可写）
MT_MAPPING_ACTOR_FILE=/data/mapping/mapping_actor.xml
MT_MAPPING_INFO_FILE=/data/mapping/mapping_info.xml
MT_GENRE_DICT=/data/mapping/genre_dict.json

# 2) 控制台（不装控制台可以不配这一段）
MT_MANAGE=true
MT_MANAGE_EMBY_URL=http://<emby-host>:8096   # Emby 默认端口 8096，改过端口按实际填
MT_MANAGE_EMBY_API_KEY=<emby api key>        # Emby → 设置 → 高级 → API 密钥
MT_MANAGE_EMBY_LIBRARIES=<library id>        # 逗号分隔；留空表示不限定媒体库
```

- 端口的默认值：后端 `8080`，Emby `8096`。本项目没有规定 Emby 端口，模板里写的是默认值。
- `MT_TOKEN` 留空表示不鉴权，插件 Token 也留空即可（仅限内网）。
- 映射表目录**必须可写**：流派词典、刷新进度、负缓存都写在这里。
- `MT_EMBY_PLUGIN_DIR` 指向 Emby 插件配置目录（只读挂载），后端据此读取翻译设置与运行开关；不挂载则只是读不到插件设置，映射仍工作。
- `MT_DISABLE_SUMMARY`、`MT_ACTOR_FALLBACK`、`MT_MAPPING_SYNC_INTERVAL` 等是启动默认值，运行期以插件设置页为准。

健康检查：

```yaml
healthcheck:
  test: ["CMD", "wget", "-qO-", "http://127.0.0.1:8080/healthz"]
  interval: 30s
  timeout: 5s
  retries: 3
```

构建镜像（需要 Docker）：`make image`。

## 控制台

`http://<host>:8080/ui`

| 页签 | 用途 |
|---|---|
| 库体检 | 全库扫描、按文件夹筛选、问题清单分页与勾选重刮、批量刷新、清空元数据 |
| 数据源与映射同步 | 基础设置只读展示、演员映射表手动同步 |
| 日志 | 按番号 / 级别 / 来源过滤处理时间线 |

## HTTP API

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/healthz` | 存活探针（无鉴权） |
| GET | `/ui`、`/ui/` | 控制台页面 |
| GET | `/ui/api/status` | Emby 连接与映射表状态 |
| GET | `/ui/api/audit` | 全库体检 |
| POST | `/ui/api/fix` | `{"action":"refresh"\|"retitle","ids":[...],"replace_all":bool}` |
| POST | `/ui/api/refresh/start` | 批量刷新或清空（`mode=wipe`） |
| POST | `/ui/api/refresh/stop` | 停止当前批次 |
| GET | `/ui/api/refresh/status` | 派发与后台完成进度 |
| POST | `/ui/api/refresh/reset` | 重置某范围的续跑进度 |
| GET | `/ui/api/paths` | 可选文件夹列表 |
| POST | `/ui/api/mapping/sync` | 立即同步在线映射表 |
| GET | `/ui/api/logs` | 处理日志 |
| GET | `/ui/api/settings` | 插件设置镜像（只读） |
| GET | `/ui/api/plugin-translation` | 翻译联动状态 |
| POST | `/ui/api/uncensored-badge` | `{"exclude":[...],"dry_run":bool}` 无码破解标签 |

刮削接口与上游一致：`/v1/movies/{provider}/{id}`、`/v1/search`、`/v1/actors/...`、`/v1/translate` 等，见上游文档。

## Emby 插件侧

1. `Server` 指向 `http://<host>:8080`。
2. `Token` 留空（后端未启用鉴权时）。
3. `TranslationMode` 必须非 `Disabled`：标题与简介由插件翻译。
4. 数据源过滤器按需要填写，常用组合是 `fanza,javbus,fc2`，与映射/翻译无关。
5. 用定制版插件时，设置页另有「隐藏作品简介 / 演员兜底 / FANZA 冷却 / JavDB 冷却」四项；用原版插件则把同名键直接写进 `MetaTube.json`。

## 数据源

- **JavDB**：默认关闭（代码里 `Priority=0`，域名被 Cloudflare 限速）。作为长尾兜底启用时，只需要给一个正数优先级：

  ```yaml
  MT_PROVIDER_JAVDB__PRIORITY=900   # 任意正数即可，数字越大越优先
  ```

  - **冷却不用跟着写**：本项目把 JavDB 的默认冷却定为 **10s**（代码内建与插件默认值都是 10s），一般情况够用。确需调整时二选一：
    `MT_PROVIDER_JAVDB__RATE_LIMIT=<间隔>`（启动值）或插件页的「JavDB 冷却间隔」（运行期生效并覆盖启动值）。
  - 也就是说冷却有三层：代码默认 10s → 环境变量启动值 → 插件页运行期值（最后生效）。只改一处就行，别两处各写一个数。
  - **禁用 ≠ 兜底失效**：即使 JavDB 处于禁用状态，`EnableActorFallback`（默认开）仍会直接调用它查演员；`Priority` 只决定它是否作为候选刮削源参与搜索。
- **FANZA**：既接受 DMM content id，也接受普通番号（含 `START-015`、`T-38027` 这类带工作室前缀的 cid，走搜索回退）。冷却默认 0（不限制），可在插件页调。
- **限速**：所有 provider 共用一张冷却表；运行期以插件页的 `FanzaRateLimit` / `JavdbRateLimit` 为准。

## 开发

```bash
make check     # gofmt + go vet + 离线单元测试
make server    # 编译二进制到 build/
make image     # 构建 metatube-plus:latest
```

- Go 1.26+；CI 会跑 `go test` 与 `golangci-lint`（配置见 `.golangci.yaml`）。
- 单元测试覆盖 `common/enrich`（映射、词典、兜底）、`common/throttle`、`common/number` 等离线逻辑。
- 新增或修改代码时保持：注释英文、`gofmt`/`gofumpt` 干净、`go vet` 通过。

## 目录结构

| 路径 | 内容 |
|---|---|
| `cmd/` | 进程入口、启动参数与装配 |
| `engine/` | 刮削编排、缓存、图片处理 |
| `provider/` | 各数据源实现（含 `javdb`、`fanza`） |
| `common/enrich/` | 中文化管线（映射、流派词典、简介处理） |
| `common/mapping/` | XML 映射表加载与在线同步 |
| `manage/` | 运维控制台后端与内嵌前端（`manage/web/index.html`） |
| `deploy/` | 部署模板与插件配置示例 |
| `translate/` | 翻译引擎与流派兜底 |

## 升级上游

```bash
git fetch upstream
git merge upstream/main
# 确认 engine/register.go 保留了 provider/javdb 的 import
make check
```

上游在 #389 删除了 `provider/javdb`，本 fork 恢复了它。**同步时务必保留 `engine/register.go` 中那一行 import**，否则演员兜底和 JavDB 源会消失。

## Credits 与许可

- 上游项目：[metatube-community/metatube-sdk-go](https://github.com/metatube-community/metatube-sdk-go)（Apache-2.0），本仓库基于其代码。
- 在线演员映射源：[li-peifeng/Jav-Actors-Mapping](https://github.com/li-peifeng/Jav-Actors-Mapping)。
- 依赖清单见 `go.mod`；主要框架为 colly、gin、gorm。
- 许可证：[Apache-2.0](LICENSE)。
