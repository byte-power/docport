# docport

将自建 Confluence 文档导出、清洗、提取关键词、（可选）向量化，以纯文件形式提供给无法直接访问 Confluence 的 AI Agent（Claude Code、OpenCode 等）使用。

## 架构

```
┌─ 能访问 Confluence 的机器（定时任务）──────────────────────┐
│                                                            │
│  Confluence REST API                                       │
│        │ docport export       （增量拉取，按 version 跳过） │
│        ▼                                                   │
│  data/raw/<SPACE>/<pageID>.json   原始 storage XHTML+元数据 │
│        │ docport clean                                     │
│        ▼                                                   │
│  data/docs/<SPACE>/<标题>-<id>.md  Markdown + frontmatter  │
│  data/docs/INDEX.md                全库目录（标题+关键词）  │
│        │ docport index                                     │
│        ▼                                                   │
│  data/index/chunks.jsonl   分块 + 关键词 + 向量(可选)       │
│  data/index/docs.json      文档编目                         │
└──────────────┬─────────────────────────────────────────────┘
               │ rsync / git push / 对象存储同步 data/ 目录
               ▼
┌─ Agent 所在机器（无 Confluence 网络）───────────────────────┐
│  方式 A：Agent 直接读 data/docs/*.md（grep + INDEX.md 目录）│
│  方式 B：docport mcp     → Claude Code / OpenCode MCP 工具  │
│  方式 C：docport serve   → HTTP 检索 API (:8787)            │
└─────────────────────────────────────────────────────────────┘
```

## 流水线说明

| 步骤 | 命令 | 说明 |
|---|---|---|
| 导出 | `docport export` | REST API 按空间分页拉取（`body.storage` + 版本 + 标签 + 祖先），`raw/manifest.json` 记录版本号实现增量 |
| 清洗 | `docport clean` | Confluence storage XHTML（含 `ac:` 宏）→ Markdown：代码宏→围栏代码块、info/warning→引用块、表格→管道表、任务列表→checkbox、页面链接→`[标题]`；toc 等导航宏丢弃 |
| 索引 | `docport index` | 按标题层级分块（默认 ≤1800 字符）；gse 中文分词 + 语料级 TF-IDF 提取文档/分块关键词；可选调 OpenAI 兼容接口生成向量 |
| 检索 | `search` / `serve` / `mcp` | 两种进程内后端可选：`bm25`（BM25 关键词，配置了 embedding 时叠加余弦向量检索并用 RRF 融合）；`bleve`（[bleve](https://github.com/blevesearch/bleve) 内存全文索引，无需向量化） |

## 快速开始

```bash
go build -o docport ./cmd/docport

cp config.example.yaml config.yaml   # 修改 base_url / token / spaces
export CONFLUENCE_TOKEN=xxx

./docport all            # export + clean + index 一次跑完
./docport search "部署流程"   # 命令行验证检索
```

首次接入时建议先导出单页验证配置与清洗效果（支持页面 ID 或各种页面 URL）：

```bash
./docport export -page 123456
./docport export -page "https://wiki.example.com/pages/viewpage.action?pageId=123456"
./docport export -page "https://wiki.example.com/display/DEV/页面标题"
./docport clean && cat data/docs/DEV/*.md   # 检查转换结果
```

只导出某棵页面树（起始页 + 全部子孙页面，适合只需要空间里某个手册目录的场景）：

```bash
./docport export -page 123456 -recursive
```

定时同步（crontab 示例，每天 6 点增量导出并同步给 Agent 机器）：

```cron
0 6 * * * cd /opt/docport && ./docport all && rsync -a --delete data/ agent-host:/opt/confluence-docs/data/
```

## 向量化（可选）

任何 OpenAI 兼容 `/v1/embeddings` 服务均可（Ollama、vLLM、OneAPI 等）。中英混合文档推荐 `bge-m3`：

```bash
ollama pull bge-m3
```

`config.yaml` 中 `embedding.enabled: true` 后重跑 `./docport index`。不启用向量时检索退化为纯 BM25，依然可用。注意：查询侧向量化也走同一接口，因此 **Agent 机器上使用向量检索时需要本地可访问的 embedding 服务**；若没有，保持 `enabled: false` 即可。

## Agent 接入

### 方式 A：直接读文件（零依赖，推荐兜底）

把 `data/docs/` 同步到 Agent 可见的目录，在项目 `CLAUDE.md` / `AGENTS.md` 中加入：

```markdown
## 内部文档
公司 Confluence 文档镜像位于 /opt/confluence-docs/data/docs/，
先查目录 INDEX.md（含每篇文档的关键词），再打开对应 .md 文件。
```

### 方式 B：MCP（Claude Code / OpenCode）

MCP server 支持两种传输，均提供三个工具：`search_docs`（检索）、`read_doc`（按 doc_id 读全文）、`list_docs`（按空间列目录）。

**B1. stdio（Agent 与文档同机）**

Claude Code `.mcp.json`：

```json
{
  "mcpServers": {
    "confluence-docs": {
      "command": "/opt/confluence-docs/docport",
      "args": ["mcp", "-config", "/opt/confluence-docs/config.yaml"]
    }
  }
}
```

OpenCode `opencode.json`：

```json
{
  "mcp": {
    "confluence-docs": {
      "type": "local",
      "command": ["/opt/confluence-docs/docport", "mcp", "-config", "/opt/confluence-docs/config.yaml"]
    }
  }
}
```

**B2. HTTP（Streamable HTTP，跨机器网络调用）**

文档所在机器上启动（一次服务，多个 Agent 共享；绑定非 127.0.0.1 时务必加 token）：

```bash
export MCP_TOKEN=$(openssl rand -hex 16)
./docport mcp -engine bleve -addr 0.0.0.0:8788 -token $MCP_TOKEN
# token 也可写进 config.yaml 的 serve.mcp_token（支持 ${ENV} 展开）
```

Claude Code `.mcp.json`：

```json
{
  "mcpServers": {
    "confluence-docs": {
      "type": "http",
      "url": "http://docs-host:8788/mcp",
      "headers": { "Authorization": "Bearer <MCP_TOKEN>" }
    }
  }
}
```

OpenCode `opencode.json`：

```json
{
  "mcp": {
    "confluence-docs": {
      "type": "remote",
      "url": "http://docs-host:8788/mcp",
      "headers": { "Authorization": "Bearer <MCP_TOKEN>" }
    }
  }
}
```

实现说明：无状态 Streamable HTTP —— JSON-RPC POST 到 `/mcp` 返回 `application/json`，通知返回 202，兼容 2025-03-26 的批量请求；不提供服务端 SSE 推送（GET 返回 405，规范允许，工具型 server 不需要）。`/healthz` 可用于探活。

### 方式 C：HTTP API

```bash
./docport serve -addr 127.0.0.1:8787
curl "http://127.0.0.1:8787/search?q=部署流程&k=5"
curl "http://127.0.0.1:8787/doc?id=1001"      # 页面 ID 或 docs/... 路径
curl "http://127.0.0.1:8787/docs?space=DEV"
```

### 检索后端：内置 BM25 或 bleve

`search` / `serve` / `mcp` 支持两种检索后端，通过 `serve.engine` 配置或 `-engine` 参数选择。两者都是**进程内**引擎——启动时把全部文档文本载入内存，无任何外部服务依赖：

- **bm25**（默认）：自研 BM25 + gse 分词；若索引带向量且 embedding 可用，叠加余弦向量检索并用 RRF 融合。
- **bleve**：基于 [bleve](https://github.com/blevesearch/bleve)（纯 Go 嵌入式全文检索库）的内存索引，**不依赖向量化**。使用 CJK 分析器（中文 bigram 分词），标题/关键词/章节字段加权（3/2/2 倍于正文），支持多词查询和相关性打分。

```bash
./docport serve -engine bleve     # HTTP API
./docport mcp -engine bleve       # MCP server
./docport search -engine bleve "回滚流程"   # 命令行验证
```

或在 config.yaml 里持久化：

```yaml
serve:
  engine: bleve
```

## 产物格式

`data/docs/**.md` frontmatter：

```yaml
---
title: 服务部署指南
space: DEV
page_id: "1001"
url: https://confluence.example.com/pages/viewpage.action?pageId=1001
version: 3
updated: "2026-07-01T12:00:00.000+08:00"
labels: [deploy, k8s]
path: [运维手册]        # Confluence 页面树面包屑
---
```

`data/index/chunks.jsonl` 每行一个分块：`{id, doc_id, space, title, section, path, url, text, keywords, vector?}`，可直接导入其他向量库（Qdrant / Milvus / pgvector）。

## 已知边界

- 只导出 `current` 状态的页面正文，不下载附件/图片（Markdown 中保留 `![attachment](文件名)` 占位）
- 已删除的 Confluence 页面不会自动从 raw/docs 中清理（可整目录重建：删 `data/` 后重跑 `all`）
- 表格中合并单元格（colspan/rowspan）按普通单元格展开
