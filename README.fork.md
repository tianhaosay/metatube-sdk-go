# metatube-plus（fork）使用说明

这是 `metatube-community/metatube-sdk-go` 的功能增强版，面向「Emby + 飞牛/NAS + 国内网络」的单人使用场景。
**对 Emby 插件的兼容性不变**：插件装在 Emby 侧，Token 留空即可直接用。

---

## 一、它比原版多了什么

| 能力 | 说明 |
|---|---|
| 元数据中文化管线 | 演员名、流派、简介、标题统一处理，**对所有刮削源生效** |
| 演员映射表 | 在线同步 avdb 源（26k 条），本地热重载 |
| 流派词典 | 映射表未覆盖的日文标签自动翻译并落盘，之后零成本 |
| 翻译 | 主引擎可选（百度/DeepL/OpenAI/Google），**次引擎固定 Google 免费** |
| 演员兜底 | DMM 没标演员时问 JavDB（JavBus 是照搬 DMM 的，没用） |
| 全库体检 | 5 类问题：标题缺失/日文、演员日文、无演员、流派日文、无封面 |
| 批量修复 | 重刮、演员改名；合并有回滚日志 |
| 演员管理 | 重复检测（带影片清单证据）、原生合并（**不需要 Emby 插件**）、跳过名单、资料补全 |
| JavDB 源 | 内置但**默认关闭**（优先级 0），需要时显式开启 |

---

## 二、部署（docker compose）

镜像：`metatube-plus:latest`（本项目 `make image` 构建）
模板：`metatube-workspace/deploy/docker-compose.fork.yml`

最小可用环境变量：

```yaml
MT_MAPPING_ACTOR_FILE=/data/mapping/mapping_actor.xml
MT_MAPPING_INFO_FILE=/data/mapping/mapping_info.xml
MT_GENRE_DICT=/data/mapping/genre_dict.json
MT_TRANSLATE=true
MT_TRANSLATE_ENGINES=baidu          # 主引擎；次引擎自动为 googlefree
MT_ACTOR_FALLBACK=JavDB
MT_MANAGE=true
MT_MANAGE_EMBY_URL=http://<emby>:8098
MT_MANAGE_EMBY_API_KEY=<emby api key>
MT_MANAGE_EMBY_LIBRARIES=<library id>   # JAV 库，例如 25765
```

- **`MT_TOKEN` 留空** = 不鉴权，插件无需填 Token，装好即可用（仅限内网）
- 映射表目录必须**可写**：词典、GUI 设置、合并日志、忽略名单都写在这里

### 健康检查

```yaml
healthcheck:
  test: ["CMD", "wget", "-qO-", "http://127.0.0.1:8080/healthz"]
  interval: 30s
  timeout: 5s
  retries: 3
```

---

## 三、控制台

`http://<host>:8080/manage/`

| 页签 | 用途 |
|---|---|
| 查询 | 输入番号，看它在每个环节的真实结果与耗时 |
| 日志自查 | 按番号/级别/来源过滤处理时间线 |
| 演员管理 | 重复检测（含影片清单）、合并/跳过、头像与简介补全 |
| 设置 | 主翻译引擎、冷却、隐藏简介 |

### 主要接口

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/healthz` | 存活探针（无鉴权） |
| GET | `/manage/api/status` | 映射表 / Emby 连接状态 |
| GET | `/manage/api/audit` | 全库体检（5 类问题） |
| POST | `/manage/api/fix` | `{"action":"refresh"|"rename_actors","limit":N}` |
| GET | `/manage/api/duplicates` | 重复演员（含每人的影片清单） |
| POST | `/manage/api/duplicates/resolve` | 合并单组 |
| POST | `/manage/api/duplicates/resolve-all` | 合并全部（写回滚日志） |
| POST | `/manage/api/duplicates/rollback` | 撤销上次合并 |
| POST | `/manage/api/duplicates/ignore` | 跳过某组 |
| POST | `/manage/api/avatars/complete` | 补头像/简介/TMDB ID |
| GET | `/manage/api/logs` | 处理日志 |
| GET/POST | `/manage/api/settings` | 运行时设置 |

---

## 四、Emby 插件侧

1. 插件 `Server` 指向 `http://<host>:8080`
2. `Token` **留空**（后端未启用鉴权）
3. 「翻译模式」必须保持 **Disabled**——翻译由后端完成，避免翻两次
4. 「数据源过滤器」保持 `fanza,javbus,fc2` 即可（与映射/翻译无关）

---

## 五、开发

```bash
make check     # gofmt 检查 + go vet + 单元测试
make image     # 构建 metatube-plus:latest
make server    # 只编译二进制到 build/
```

单元测试覆盖：`common/enrich`（映射/翻译/词典/兜底）、`common/throttle`。

---

## 六、注意

- 本 fork 与上游分叉较大（映射/翻译/控制台），升级上游需手工合并
- 「替换全部元数据」在 Emby 侧**永远不要点**，会覆盖本地中文元数据
- 合并演员前请先看「影片清单」证据；同名不同人的情况要用「跳过」
