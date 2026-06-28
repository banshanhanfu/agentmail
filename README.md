# AgentMail

> **Agent 专用邮件引擎** — 让 AI Agent 拥有自己的邮箱。

AgentMail 是为 AI Agent 设计的轻量级邮件系统，不是给人类用的。
没有 Web 界面、没有 IMAP/POP3、没有密码登录 —— 只有 REST API。

## 设计哲学

传统邮件服务器（Postfix、Dovecot、PMail 等）是为人类使用设计的：
多用户、密码登录、Web 界面、IMAP 客户端同步……

AgentMail 反过来设计：

| 场景 | 传统邮件 | AgentMail |
|------|---------|-----------|
| 用户 | 人类（密码+IMAP） | Agent（Token+API） |
| 界面 | Web UI / 邮件客户端 | REST API |
| 身份认证 | 用户名+密码 | Bearer Token |
| 多实例 | 一套端口只能跑一个 | 每个 Agent 独立 API Token |
| 自动处理 | 需要外挂过滤器 | 内置规则引擎 + Webhook |
| 嵌入使用 | 不能 | 可作为 Go 库 import |

## 快速开始

### 1. 下载

```bash
# 从 GitHub Releases 下载
wget https://github.com/banshanhanfu/agentmail/releases/download/v0.1.0/agentmail-linux-amd64
chmod +x agentmail-linux-amd64
sudo mv agentmail-linux-amd64 /usr/local/bin/agentmail
```

### 2. 配置

创建 `config.yaml`：

```yaml
smtp_listen: ":25"          # SMTP 收信端口（需 root）
http_listen: ":8081"        # HTTP API 端口
data_dir: "/data/agentmail" # 数据目录
domain: "wsai.chat"         # 默认域名
log_level: "info"
```

### 3. 运行

```bash
# 直接运行
agentmail -config config.yaml

# 查看版本
agentmail -version
```

### 4. 注册身份

```bash
# 注册一个 Agent 邮箱身份
curl -X POST http://localhost:8081/v1/identities \
  -H "Authorization: Bearer *** \
  -d '{"email":"kefu@wsai.chat","name":"文殊客服"}'

→ {"email":"kefu@wsai.chat","token":"am_abc123..."}
```

> ⚠️ 以上命令中的 `am_admin_token` 是初始 admin Token（从日志中获取），
> 第一个身份会自动获得 admin 权限。

### 5. 发邮件

```bash
curl -X POST http://localhost:8081/v1/outbox \
  -H "Authorization: Bearer *** \
  -d '{"from":"kefu@wsai.chat","to":["user@example.com"],
       "subject":"您好","body":"这是一封由 Agent 发送的邮件"}'
```

### 6. 收邮件

```bash
# 查收件箱
curl http://localhost:8081/v1/mailbox/inbox?limit=10 \
  -H "Authorization: Bearer ***"

# 查看详情
curl http://localhost:8081/v1/mailbox/inbox/1 \
  -H "Authorization: Bearer ***"

# 搜索
curl "http://localhost:8081/v1/search?q=关键词" \
  -H "Authorization: Bearer ***"
```

## REST API 文档

### 身份管理

| 方法 | 路径 | 说明 |
|------|------|------|
| `POST` | `/v1/identities` | 创建身份（Agent 邮箱） |
| `GET` | `/v1/identities` | 列出所有身份 |
| `DELETE` | `/v1/identities/{email}` | 删除身份 |

### 邮件

| 方法 | 路径 | 说明 |
|------|------|------|
| `GET` | `/v1/mailbox/{folder}` | 邮件列表（inbox/sent/trash） |
| `GET` | `/v1/mailbox/{folder}/{id}` | 查看单封邮件 |
| `DELETE` | `/v1/mailbox/{folder}/{id}` | 删除邮件 |
| `PATCH` | `/v1/mailbox/{folder}/{id}/flags` | 更新标记（seen/flagged） |
| `POST` | `/v1/mailbox/{folder}/{id}/move` | 移动文件夹 |
| `GET` | `/v1/search?q=关键词` | 搜索邮件 |
| `GET` | `/v1/stats` | 统计概览 |

**查询参数：**
- `limit` — 每页数量（默认 50，最大 200）
- `offset` — 偏移量

### 发件

| 方法 | 路径 | 说明 |
|------|------|------|
| `POST` | `/v1/outbox` | 发送邮件 |

**发件请求体：**
```json
{
  "from": "kefu@wsai.chat",
  "to": ["user@example.com"],
  "cc": ["cc@example.com"],
  "subject": "您好",
  "body": "纯文本正文",
  "body_html": "<p>HTML正文(可选)</p>"
}
```

### 规则引擎

| 方法 | 路径 | 说明 |
|------|------|------|
| `GET` | `/v1/rules` | 列出规则 |
| `POST` | `/v1/rules` | 创建规则 |
| `PUT` | `/v1/rules/{id}` | 更新规则 |
| `DELETE` | `/v1/rules/{id}` | 删除规则 |

**规则动作：**
- `webhook` — 新邮件到达时 POST JSON 到指定 URL
- `reply` — 自动回复（支持 {{from}}、{{subject}} 模板变量）
- `forward` — 转发到指定地址
- `tag` — 添加标签
- `discard` — 丢弃（移入 trash）

## 作为 Go 库使用

```go
import "github.com/banshanhanfu/agentmail"
import "github.com/banshanhanfu/agentmail/internal/sqlitestore"

// 初始化
store, _ := sqlitestore.New("/data/agentmail.db")
defer store.Close()

// 注册身份
store.CreateIdentity("bot@wsai.chat", "MyBot", "token123", "", "wsai.chat")

// 保存邮件
id, _ := store.SaveMessage(&agentmail.Message{
    Identity:  "bot@wsai.chat",
    Folder:    "inbox",
    From:      "someone@example.com",
    Subject:   "您好",
    BodyText:  "这是一封邮件",
    ReceivedAt: time.Now().Unix(),
})

// 搜索
msgs, _ := store.SearchMessages("bot@wsai.chat", "关键词", 20, 0)
```

## 部署建议

### 生产环境

```bash
# 创建用户
useradd -r -s /bin/false agentmail

# 创建数据目录
mkdir -p /data/agentmail
chown agentmail:agentmail /data/agentmail

# systemd 服务
cat > /etc/systemd/system/agentmail.service << 'EOF'
[Unit]
Description=AgentMail — Agent 专用邮件引擎
After=network.target

[Service]
Type=simple
User=agentmail
Group=agentmail
ExecStart=/usr/local/bin/agentmail -config /etc/agentmail/config.yaml
Restart=on-failure
RestartSec=5
LimitNOFILE=65536

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
systemctl enable --now agentmail
```

### 防火墙

```bash
# 25 端口（收信）+ API 端口
firewall-cmd --permanent --add-port=25/tcp
firewall-cmd --permanent --add-port=8081/tcp
firewall-cmd --reload
```

### DNS 配置

确保域名有正确的 MX 记录指向服务器 IP：

```
wsai.chat.  IN  MX  10  mail.wsai.chat.
mail.wsai.chat.  IN  A   116.204.132.105
```

### DKIM

```go
// 用 Go 生成 DKIM 密钥对
import "github.com/banshanhanfu/agentmail/internal/dkim"

pub, priv, _ := dkim.GenerateKeyPair()
// 把 pub 加到 DNS TXT 记录
// 把 priv 存到 Identity.SignKey
```

DNS TXT 记录：
```
agentmail._domainkey.wsai.chat.  IN  TXT  "v=DKIM1; k=ed25519; p=..."
```

## 开发

```bash
# 克隆
git clone https://github.com/banshanhanfu/agentmail.git
cd agentmail

# 编译
go build -ldflags="-s -w" ./cmd/agentmail

# 测试
go test ./...

# 交叉编译
GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o agentmail-linux-amd64 ./cmd/agentmail
GOOS=linux GOARCH=arm64 go build -ldflags="-s -w" -o agentmail-linux-arm64 ./cmd/agentmail
```

## 协议

MIT License — 详见 [LICENSE](LICENSE)

Copyright (c) 2026 banshanhanfu
