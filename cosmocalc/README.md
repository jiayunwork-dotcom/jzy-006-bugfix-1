# cosmocalc — 宇宙学红移与哈勃距离核算服务

纯服务端计算组件：上游提交光谱观测数据，服务算出**红移量**、**退行速度**与低红移近似下的**共动距离**，并判定结果是否落在非相对论线性区。每次核算的请求与结果都会持久化，可供历史查询。

## 快速开始

```bash
docker compose up --build
```

服务监听 `:8080`，PostgreSQL 随之启动并自动建表。无 `DATABASE_URL` 时服务退化为内存存储（便于本地开发，历史不持久）。

```bash
# 本地开发（需 Go 1.22+）
go test ./...        # 运行全部自动化测试
go run ./cmd/server  # 内存存储模式启动
```

## 物理约定（固定，不混用）

| 量 | 单位 |
|---|---|
| 距离 | Mpc（兆秒差距） |
| 速度 | km/s |
| 哈勃常数 | km/s/Mpc |
| 波长 | 任意单位，静止/观测一致即可（仅比值参与计算） |

- 红移定义：`z = λ_obs / λ_rest − 1`（**静止波长做分母**）。`λ_obs > λ_rest` 为红移（z > 0），反之为蓝移（z < 0），z = 0 时速度、距离严格为零。
- 低红移线性区（默认关系 `cosmological_linear`）：`v = c·z`，`D = v / H₀`，c = 299792.458 km/s。
- 可选相对论多普勒关系（`relativistic: true`，标签 `relativistic_doppler`）：`β = ((1+z)²−1)/((1+z)²+1)`，反变换 `z = √((1+β)/(1−β)) − 1`。两套关系是独立代码路径，由开关显式选择，绝不混合。
- 线性阈值固定为 **z = 0.1**：超过后仍返回线性近似值，但响应带 `beyond_linear_threshold: true` 与 `warning`。
- **蓝移不做距离查询**：z < 0 时返回 422 结构化错误 `blueshift_distance`，绝不冒充朝前方的正距离。

## API

### `POST /api/v1/redshift` — 计算红移

输入三选一：`rest_wavelength`+`observed_wavelength` / `redshift` / `velocity_kms`；可选 `relativistic`（默认 false）。

```bash
curl -X POST localhost:8080/api/v1/redshift \
  -d '{"rest_wavelength":656.28,"observed_wavelength":675.9684}'
```
```json
{"redshift":0.03,"velocity_kms":8993.77,"shift_type":"redshift","relation":"cosmological_linear"}
```

### `POST /api/v1/distance` — 计算距离与速度并判定线性区

在红移接口输入之上**必须**提供 `hubble_constant`。

```bash
curl -X POST localhost:8080/api/v1/distance -d '{"redshift":0.5,"hubble_constant":70}'
```
```json
{"redshift":0.5,"velocity_kms":149896.229,"shift_type":"redshift",
 "relation":"cosmological_linear","hubble_constant":70,"distance_mpc":2141.3747,
 "linear_regime":false,"beyond_linear_threshold":true,"linear_threshold":0.1,
 "warning":"redshift exceeds the linear-regime threshold; linear approximation returned but no longer reliable"}
```

### `POST /api/v1/batch` — 批量计算

```bash
curl -X POST localhost:8080/api/v1/batch -d '{"items":[
  {"id":"h-alpha","rest_wavelength":656.28,"observed_wavelength":675.9684,"hubble_constant":70},
  {"id":"approaching","rest_wavelength":600,"observed_wavelength":500,"hubble_constant":70}
]}'
```
每项独立返回 `ok` + `result` 或结构化 `error`，单项失败不影响其他项。**每条谱线（含失败项）都会和单条接口一样作为一条独立的 `distance` 历史记录落库**，因此可按 `type=distance` 逐条检索；批量接口本身不产生额外的汇总记录。

### `GET /api/v1/history?type=distance&limit=50&offset=0` — 历史查询

按类型（`redshift`/`distance`）过滤，按时间倒序返回持久化的请求与结果。

### `GET /api/v1/demo` — 预置算例

Hα 线（静止 656.28 nm）在 675.9684 nm 被观测（z = 0.03），H₀ = 70 km/s/Mpc。手算：v = c·z = 8993.77 km/s，D = v/H₀ = **128.48 Mpc**，与服务返回值一致。

### `GET /healthz` · `GET /status` — 运行状态

`/status` 返回运行时长、累计请求数、数据库连通性、线性阈值与单位约定，供监控采集。

## 错误响应

所有非法输入返回结构化错误并区分类型：

```json
{"error":{"type":"non_positive_hubble_constant","message":"...","field":"hubble_constant"}}
```

| type | HTTP | 含义 |
|---|---|---|
| `invalid_body` | 400 | 请求体不是合法 JSON |
| `missing_field` | 400 | 缺少必要字段（如 hubble_constant、成对波长缺其一） |
| `non_positive_wavelength` | 400 | 静止/观测波长非正 |
| `non_positive_hubble_constant` | 400 | 哈勃常数非正 |
| `redshift_out_of_range` | 400 | z ≤ −1 或非有限值 |
| `velocity_out_of_range` | 400 | 相对论模式下 \|v\| ≥ c 或非有限值 |
| `blueshift_distance` | 422 | 对蓝移（z < 0）发起距离查询 |
| `internal` | 500 | 服务内部错误 |

## 测试覆盖

`go test ./...`（含 `-race` 验证）覆盖全部关键行为：

- 红移定义：观测波长加倍 → z′ = 2z + 1（而非 2z）；静止波长为分母
- 距离与哈勃常数成反比（H₀ 加倍 → D 减半）
- 零红移 → 零速度、零距离
- 超线性阈值被显式标记并附警告
- 蓝移被标记为 blueshift 并以 422 拒绝，绝不返回距离
- 相对论开关与默认线性关系互不混用（z=1：线性 v=c，相对论 v=0.6c；往返变换自洽）
- 批量计算混合项（成功/蓝移/非法 H₀）各自独立返回
- 历史持久化：请求落库、按类型过滤、倒序
- 批量中每条谱线（含失败项）都作为独立的 `distance` 记录落库，可按类型逐条检索；批量与单条并发混发时历史条数恰好等于核算次数，不丢不重不串
- 并发 64 路请求结果互不串扰、历史记录不重不漏

## 结构

```
cmd/server/          入口：装配存储与 HTTP 服务，优雅退出
internal/calc/       纯计算核心（红移/速度/距离，零依赖，并发安全）
internal/service/    编排层：输入解析、蓝移拦截、线性区判定
internal/store/      持久化：PostgreSQL（生产）+ 内存（测试）
internal/api/        HTTP 处理器、结构化错误、状态端点
Dockerfile           多阶段构建（非 root 运行）
docker-compose.yml   一键启动服务 + PostgreSQL（健康检查、数据卷）
```
