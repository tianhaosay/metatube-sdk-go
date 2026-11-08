# metatube-plus

`metatube-community/metatube-sdk-go` 的功能增强版，面向「Emby + NAS + 国内网络」的单人使用场景。
对 Emby 插件的兼容性保持不变：插件装在 Emby 侧，后端 Token 留空即可直接使用。

## 功能

| 能力 | 说明 |
|---|---|
| 元数据中文化管线 | 演员名、流派、简介统一处理，对所有刮削源生效；标题与简介的翻译由 Emby 插件负责 |
| 演员映射表 | 从 GitHub 在线同步（约 2.6 万条），本地文件变化后热重载 |
| 流派词典 | 映射表未覆盖的日文标签翻译一次后落盘，之后不再调用引擎 |
| 演员兜底 | 主源没有演员时向 JavDB 查询一次，失败结果缓存 12 小时 |
| 全库体检 | 6 类问题（标题缺失 / 标题日文 / 演员日文 / 无演员 / 流派日文 / 无封面）+ 演员未收录提示 |
| 批量修复 | 重刮、标题修正、清空元数据；分批、可暂停、进度可续跑 |
| 无码破解标签 | 给文件名带 `-LTX` / `-U` / `-UC` 的影片追加「无码破解」类型 |
| 运维控制台 | `/ui`，3 个页签：库体检 / 数据源与映射同步 / 日志 |
| JavDB 源 | 内置但默认关闭（Priority 0），需要时按下方示例开启 |

## 翻译与配置的分工

只有一处需要配置：Emby → 插件 → MetaTube 设置页。后端通过只读挂载读取同一份
`MetaTube.json`，约 15 秒热加载，不需要重启。

| 内容 | 负责方 |
|---|---|
| 标题 / 简介 | Emby 插件（`TranslationMode` 必须非 Disabled） |
| 流派 / 标签 | 后端：词典优先，未命中时调用插件配置的引擎，结果写回词典 |
| 演员名 | 后端：映射表归一 |
| 简介隐藏 | 后端：清空字段，与翻译无关 |

引擎、密钥、翻译模式、隐藏简介、演员兜底、数据源冷却都在插件设置页修改；控制台「数据源与映射同步」只做只读展示。

## 部署

镜像 `metatube-plus:latest`，模板 `deploy/docker-compose.fork.yml`。

```bash
cp deploy/docker-compose.fork.yml docker-compose.yml
# 修改三个 TODO：Emby 地址、API Key、媒体库 ID
docker compose up -d
```

必需的环境变量：

```yaml
MT_MAPPING_ACTOR_FILE=/data/mapping/mapping_actor.xml
MT_MAPPING_INFO_FILE=/data/mapping/mapping_info.xml
MT_GENRE_DICT=/data/mapping/genre_dict.json
MT_MANAGE=true
MT_MANAGE_EMBY_URL=http://<emby>:8098
MT_MANAGE_EMBY_API_KEY=<emby api key>
MT_MANAGE_EMBY_LIBRARIES=<library id>
```

- `MT_TOKEN` 留空表示不鉴权，插件 Token 也留空即可（仅限内网部署）。
- 映射表目录必须可写：流派词典、刷新进度、负缓存都写在这里。
- `MT_EMBY_PLUGIN_DIR` 指向 Emby 插件配置目录（只读挂载），后端据此读取翻译设置与运行开关。
- `MT_DISABLE_SUMMARY`、`MT_ACTOR_FALLBACK`、`MT_MAPPING_SYNC_INTERVAL` 等为启动默认值，运行期以插件设置页为准。

健康检查：

```yaml
healthcheck:
  test: ["CMD", "wget", "-qO-", "http://127.0.0.1:8080/healthz"]
  interval: 30s
  timeout: 5s
  retries: 3
```

## 控制台

`http://<host>:8080/ui`

| 页签 | 用途 |
|---|---|
| 库体检 | 全库扫描、按文件夹筛选、问题清单分页与勾选重刮、批量刷新（双进度条）、清空元数据 |
| 数据源与映射同步 | 基础设置只读展示、演员映射表手动同步 |
| 日志 | 按番号 / 级别 / 来源过滤（抓取、兜底、翻译、引擎、接口） |

### 接口

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/healthz` | 存活探针（无鉴权） |
| GET | `/ui/api/status` | Emby 连接与映射表状态 |
| GET | `/ui/api/audit` | 全库体检 |
| POST | `/ui/api/fix` | `{"action":"refresh"\|"retitle","ids":[...],"replace_all":bool}` |
| POST | `/ui/api/refresh/start` | 批量刷新或清空（`mode=wipe`） |
| POST | `/ui/api/refresh/stop` | 停止当前批次 |
| GET | `/ui/api/refresh/status` | 派发与后台完成进度 |
| POST | `/ui/api/refresh/reset` | 重置某范围的续跑进度 |
| GET | `/ui/api/paths` | 可选文件夹列表 |
| POST | `/ui/api/mapping/sync` | 立即同步在线映射表 |
| GET | `/ui/api/logs` | 处理日志（番号 / 级别 / 来源） |
| GET | `/ui/api/settings` | 插件设置镜像（只读） |
| GET | `/ui/api/plugin-translation` | 翻译联动状态 |
| GET | `/ui/api/consistency` | 后端与插件设置的重叠项（供外部检查） |
| POST | `/ui/api/uncensored-badge` | `{"exclude":[...],"dry_run":bool}` 无码破解标签 |

## Emby 插件侧

1. `Server` 指向 `http://<host>:8080`。
2. `Token` 留空（后端未启用鉴权时）。
3. `TranslationMode` 必须非 `Disabled`：标题与简介由插件翻译。
4. 数据源过滤器保持 `fanza,javbus,fc2` 即可，与映射/翻译无关。

若要把 JavDB 作为长尾兜底数据源：

```yaml
MT_PROVIDER_JAVDB__PRIORITY=900
MT_PROVIDER_JAVDB__RATE_LIMIT=10s
```

## 开发

```bash
make check     # gofmt + go vet + 单元测试
make server    # 编译二进制到 build/
make image     # 构建 metatube-plus:latest
```

单元测试覆盖 `common/enrich`（映射、词典、兜底）、`common/throttle`、`provider/javdb` 等。

## 注意

- 与上游分叉较大（映射、翻译归属、控制台），升级上游需手工合并；合并时务必保留
  `engine/register.go` 中 `provider/javdb` 的 import，否则演员兜底与 JavDB 源会消失。
- 不要在 Emby 界面点「替换全部元数据」，会覆盖已有中文元数据；重刮统一走控制台。
- 清空元数据前先关闭插件「自动更新」，否则清空后会被立刻重刮覆盖（控制台会拦截并提示）。
- 合并演员等历史功能已移除，不要重新引入。
