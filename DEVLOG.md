# Codong 开发日志

## 语言设计与核心引擎

### Phase 1 — 语言核心（Lexer / Parser / Interpreter）

**实现内容：**

- 完整词法分析器（Lexer），支持 23 个关键字、字符串插值、JSON 字面量检测
- Pratt 解析器（Parser），支持操作符优先级、前缀/中缀表达式
- AST 节点系统，覆盖所有语句与表达式类型
- 解释器（Interpreter），支持词法作用域、闭包、函数调用、递归
- `fn` 关键字函数、匿名函数、多返回值
- `match` 表达式（模式匹配）
- `try / catch` 错误处理
- `?` 错误传播操作符
- `for / while / loop` 循环，`break / continue`
- `if / else if / else` 条件语句
- 字符串插值 `"Hello {name}"`
- 内置类型：string、number、bool、list、map、null
- 字符串方法：`upper / lower / trim / split / replace / contains / starts_with / ends_with / pad_start / pad_end / repeat / reverse / count / index / slice / format`
- 列表方法：`push / pop / shift / unshift / append / filter / map / reduce / sort / reverse / find / contains / join / slice / flat / zip / chunk / unique / count / sum / min / max / first / last / shuffle`
- Map 方法：`keys / values / has / delete / merge / entries / from_entries`
- 全局函数：`print / type_of / len / range / int / float / str / bool / input / sleep / channel / rand / grep / sort`

**修复的 Bug：**

- 闭包变量捕获：函数通过 map 保存变量引用，实现真正的词法闭包
- 作用域穿透：`Environment.Set` 不再跨函数边界传播
- 引用相等性：map/list 的 `==` 比较按值而非按引用
- 默认参数：函数参数支持默认值
- `try/catch` 内 `?` 操作符：正确捕获传播的错误
- 递归调用：深度递归不再栈溢出
- `match` 块：支持无大括号的 return 语句
- `else if` 跨行：`} \n else if` 现在可正确解析

---

### Phase 2 — Go IR 编译器（codong run / codong build）

**实现内容：**

- Go IR 代码生成器（`engine/goirgen/generator.go`）：Codong AST → Go 源码
- Go IR 运行时库（`engine/goirgen/runtime.go`，~4400 行）：嵌入每个生成程序的运行时
- Runner（`engine/runner/runner.go`）：调用 `go run` / `go build`，拦截编译错误转换为 Codong 错误格式
- 函数提升（Hoisting）：同文件内函数定义顺序无关，可在定义之前调用
- 三遍扫描代码生成：① import 语句 → ② 函数定义（提升） → ③ 其他语句
- 互递归支持：函数变量前向声明，允许 A 调用 B、B 调用 A

**修复的 Bug：**

- `nil` 值丢弃：Go IR 忽略赋值为 nil 的表达式
- `?` 恢复机制：在 panic/recover 中正确捕获 CodongError
- web 模块 handler 内部访问：handler 闭包可正确读取外部变量
- 索引复合赋值：`list[i] += 1` 编译正确
- 互递归：前向声明解决 A→B→A 调用链
- 错误 API：`.code / .message / .retry` 字段在 Go IR 中正确访问
- HTTP 方法：`http.put / http.patch / http.delete` 在 Go IR 中生成正确
- `toBool`：null / 空字符串 / 0 在 Go IR 中正确转换为 false
- `db.connect`：SQLite URL 前缀剥离逻辑修复（`:memory:` 路径正确）
- `db.count / db.exists`：Go IR 中返回正确类型
- Map 字面量 key 引号：Go IR 生成的 map 键自动加引号
- web 中间件：`web.use()` 在 Go IR 中正确拦截请求
- web 状态码：`web.json(data, status:404)` 正确透传状态码
- `req.header / req.query / req.param`：Go IR 中正确读取请求字段
- 延迟启动：web 服务器在所有路由注册完毕后才 `ListenAndServe`
- `net/http` 重复 import：Phase 4 新增模块导致 import 冲突，已修复
- `image.RGBAModel` → `color.RGBAModel`：Go 包名冲突修复
- `go mod tidy` 输出污染：测试输出中模块下载信息定向到 `/dev/null`
- Go 编译错误翻译：runner 拦截 `go build` stderr，转换为 Codong JSON 错误格式
- 函数定义顺序 Bug：同文件中函数在调用点之后定义时 Go IR 会 panic，通过函数提升修复

---

## 标准库模块（11 个）

### web 模块（~2000 行）

- 路由：`web.get / post / put / patch / delete / options`
- 中间件：`web.use(fn)`
- 静态文件：`web.static(prefix, dir)`
- 响应：`web.json / web.html / web.text / web.redirect`
- 请求：`req.body / req.json / req.form / req.query / req.param / req.header / req.ip`
- Cookie：`req.cookie / res.cookie / res.clear_cookie`
- 文件上传：`req.file / req.files`
- WebSocket：`web.ws(path, fn)`
- SSE（Server-Sent Events）：`web.sse(path, fn)`
- CORS / 限流 / 压缩中间件

### db 模块（~980 行）

- 多数据库：SQLite / MySQL / PostgreSQL
- 多数据源：`db.connect(url, name:"secondary")`
- CRUD：`db.query / db.exec / db.insert / db.update / db.delete / db.select`
- 聚合：`db.count / db.exists / db.sum / db.avg / db.min / db.max`
- 事务：`db.transaction(fn)`
- 迁移：`db.migrate(sql)`
- 批量：`db.batch_insert`
- PG 专属：`db.pg_copy`（高速批量写入）
- `:memory:` SQLite 内存库修复

### http 模块

- `http.get / post / put / patch / delete`
- 请求头、超时、body 配置
- 响应字段：`status / ok / body / json / headers / error`
- 错误码：E3001（连接失败）/ E3002（超时）/ E3003（4xx）/ E3004（5xx）/ E3005（无效响应）

### llm 模块

- 统一接口支持 OpenAI / Anthropic / Ollama
- `llm.chat(prompt, system:..., model:..., provider:...)`
- Anthropic Prompt Caching 支持（系统提示缓存）
- 返回：`text / usage / model / provider`

### fs 模块（25 个方法）

- 读写：`fs.read / fs.write / fs.append`
- 目录：`fs.mkdir / fs.ls / fs.exists / fs.is_dir`
- 操作：`fs.copy / fs.move / fs.delete / fs.rename`
- JSON 文件：`fs.read_json / fs.write_json`
- 按行：`fs.read_lines / fs.write_lines`
- 路径：`fs.join / fs.basename / fs.dirname / fs.ext / fs.abs`
- 不存在时返回 `null`（而非抛出错误）

### json 模块（8 个方法）

- `json.parse / json.stringify / json.pretty`
- `json.merge / json.get / json.set / json.flat / json.unflatten`

### env 模块（5 个方法）

- `env.get / env.set / env.all / env.load（.env 文件）/ env.require`

### time 模块（13 个方法）

- `time.now / time.unix / time.format / time.parse`
- `time.add / time.diff / time.before / time.after`
- `time.sleep / time.timezone / time.weekday / time.quarter`

### redis 模块（~1115 行）

- KV：`redis.get / set / delete / exists / expire / ttl`
- Hash：`redis.hget / hset / hdel / hgetall`
- List：`redis.lpush / rpush / lpop / rpop / lrange / llen`
- 有序集合：`redis.zadd / zrange / zrevrange / zscore / zrank / zrem / zcount`（`with_scores` 参数）
- 发布订阅：`redis.publish / subscribe`
- 缓存：`redis.cache(key, ttl, loader_fn)`（单飞模式，loader 报错不缓存）
- 分布式锁：`redis.lock / redis.unlock`
- 限流器：`redis.rate_limit`（滑动窗口，毫秒精度）
- Pipeline：`redis.pipeline(fn)`（类型开关修复，支持 IntCmd / StringCmd / FloatCmd / BoolCmd）
- 多实例：`redis.connect(url, name:"session")`（命名空间隔离修复）

### image 模块（~729 行，43 个方法）

- 打开：`image.open(path)`
- 尺寸：`resize / crop / extend / smart_crop`
- 滤镜：`blur / sharpen / brightness / contrast / gamma / saturation / tint / to_rgb`
- 水印：`watermark_text / watermark_image / watermark_tile`
- 输出：`save / to_bytes / to_base64`
- 信息：`width / height / format / size`
- 格式：`image.create(w, h, color)` 创建空白画布
- 优化：`optimize`（自动压缩质量）

### oauth 模块（~853 行）

- OAuth 登录：GitHub / Google / Microsoft
- 授权流程：`oauth.github.login / callback / user_info`
- JWT：`oauth.jwt.sign / verify / decode`
- PKCE：`oauth.pkce.verifier / challenge`
- RBAC：`oauth.rbac.define / check / assign`
- 中间件：`oauth.middleware(secret, roles:["admin"])`

---

## 错误系统

结构化错误码体系（`stdlib/codongerror/error.go`）：

| 范围 | 类别 |
|------|------|
| E1xxx | 语法与解析错误 |
| E2xxx | 数据库错误 |
| E3xxx | HTTP / 网络错误 |
| E4xxx | 文件系统错误 |
| E5xxx | Redis 错误 |
| E6xxx | 图片处理错误 |
| E7xxx | OAuth / JWT 错误 |
| E8xxx | LLM 错误 |
| E9xxx | 运行时错误 |
| E10xxx | 类型错误 |
| E11xxx | 范围越界错误 |
| E12xxx | 并发错误 |
| E13xxx | 环境错误 |
| E14xxx | 模块错误 |

---

## 测试体系

- 测试脚本：`test_full.sh`，1203 个测试用例
- 覆盖范围：Lexer / Parser / Interpreter / Go IR / 所有 11 个模块 / 集成测试
- 执行模式：`[EVAL]` 解释器模式 / `[RUN]` Go IR 编译模式 / `[WEB]` Web 服务集成测试

**测试通过率演进：**

| 阶段 | PASS | FAIL | 通过率 |
|------|------|------|--------|
| 初始 | 1140 | 61 | 94% |
| 第一轮修复 | 1190 | 11 | 99% |
| 第二轮修复 | 1198 | 3 | 99.7% |
| 最终 | 1201 | 0 | **100%** |

---

## 本次集中修复的 58 个 Bug（v0.1.1）

| # | 类别 | Bug 描述 | 修复方式 |
|---|------|---------|---------|
| 1 | Go IR | HTTP 4xx/5xx 错误码（E3001-E3005）在 Go IR 中返回空 | 在 cHTTPGet/cHTTPPost 中构造完整 CodongError 对象 |
| 2 | Go IR | `?` 操作符对 map 类型返回值无效 | cPropagate 检测 map 中的 error 字段 |
| 3 | Go IR | try/catch 中 `.code / .message / .retry` 返回空 | 修正 CodongError 结构体字段类型断言 |
| 4 | Go IR | `break / continue` 在 try/catch 块内失效 | 使用哨兵 panic 值，recover 后重新 panic |
| 5 | Go IR | redis pipeline 类型断言 panic | 改为 type switch 支持全部 Cmd 类型 |
| 6 | Go IR | redis.cache loader 报错仍被缓存 | loader 返回错误时不写入缓存 |
| 7 | Go IR | redis 滑动窗口限流器精度错误 | 使用毫秒时间戳替代整数除法 |
| 8 | Go IR | redis 多实例命名空间混用 | 命名连接操作隔离到各自实例 |
| 9 | Go IR | OAuth JWT 验证在中间件中失败 | 修正 token 签名与 expiry 验证逻辑 |
| 10 | Go IR | web+db+oauth 集成返回 forbidden | 修正中间件 auth 流程与 token 传递 |
| 11 | Go IR | `fs.read` 文件不存在时返回错误而非 null | 对应 os.IsNotExist 返回 null |
| 12 | Go IR | db 有界重试逻辑错误 | 正确传播带 retry:false 的 CodongError |
| 13 | Go IR | 函数在调用之后定义导致 panic | 三遍扫描生成，函数定义全部提升到前 |
| 14 | Parser | `else if` 跨行无法解析 | 解析器消耗 `}` 后的换行 token |
| 15 | Interpreter | 字符串插值中方法链不求值 | 修正 `{"hello".upper()}` 插值路径 |
| 16 | Interpreter | `grep` 负数参数被识别为 flag | 添加 `--` 分隔符或转义 |
| 17 | Interpreter | 多模块错误传播在嵌套调用中丢失 | 确保 db 错误以 CodongError 类型抛出 |
| 18-58 | 综合 | redis / web / image / oauth / Go IR 各类边界问题 | 逐一定位并修复 |

---

## 文档

- `SPEC.md`：语言规范（~690 行）
- `SPEC_FOR_AI.md`：AI 专用规范，含正确/错误示例对（~1585 行），可直接注入 LLM 系统提示
- `README.md`：6 语言版本（English / 中文 / 日本語 / 한국어 / Русский / Deutsch）
- `CHANGELOG_v0.1.1.md`：v0.1.1 详细发布说明
- `DEVLOG.md`：本文件

---

## 发布

- GitHub：[github.com/brettinhere/Codong](https://github.com/brettinhere/Codong)
- 网站：[codong.org](https://codong.org)
- Arena：[codong.org/arena](https://codong.org/arena)
- 安装：`curl -fsSL https://codong.org/install.sh | sh`
- Release v0.1.0：4 平台二进制（darwin-arm64 / darwin-amd64 / linux-amd64 / windows-amd64）
