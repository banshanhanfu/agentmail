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

### DNS 反垃圾配置

发出去的信不被扔进垃圾箱、别人发的信不被冒名伪造，靠三条 DNS 记录。**不管是哪台 Agent 部署 AgentMail，这三条缺一不可。**

#### 1. MX — 告诉全世界：@你的域名 的邮件送到哪台服务器

```dns
yourdomain.com.  IN  MX  10  mail.yourdomain.com.
mail.yourdomain.com.  IN  A    你的服务器IP
```

> `MX 10` 的 10 是优先级，越小越优先。只有一个邮件服务器时填 10 即可。

#### 2. SPF — 声明：只有我的服务器能代发 @你的域名 的邮件

防止别人用你的域名发垃圾邮件。

```dns
yourdomain.com.  IN  TXT  "v=spf1 ip4:你的服务器IP include:mail.yourdomain.com ~all"
```

参数说明：
- `ip4:你的服务器IP` — 允许你这台服务器发信
- `include:mail.yourdomain.com` — 允许 mail.yourdomain.com 发信
- `~all` — 软拒绝（其他服务器发的标记为可疑，但不直接拒收）
- `-all` — 硬拒绝（更严格，建议等 SPF 稳定后再改）

#### 3. DKIM — 数字签名：证明这封邮件确实是你发的

**第一步：生成密钥对**

```bash
# 方法一：用 Go 生成
go run -mod=mod << 'GOEOF'
package main

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"os"
)

func main() {
	priv, _ := rsa.GenerateKey(rand.Reader, 2048)
	privBytes, _ := x509.MarshalPKCS8PrivateKey(priv)
	privPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privBytes})
	os.WriteFile("dkim_private.pem", privPEM, 0600)

	pubBytes, _ := x509.MarshalPKIXPublicKey(&priv.PublicKey)
	pubPEM := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubBytes})

	pubB64 := base64.StdEncoding.EncodeToString(pubBytes)
	fmt.Println("=== DNS TXT Record ===")
	fmt.Printf("Name:  agentmail._domainkey.yourdomain.com\n")
	fmt.Printf("TTL:   3600\n")
	fmt.Printf("Type:  TXT\n")
	fmt.Printf("Value: v=DKIM1; k=rsa; p=%s\n", pubB64)
	fmt.Println("========================")
	fmt.Println("Private key saved to: dkim_private.pem")
}
GOEOF
```

**第二步：把公钥加到 DNS**

```dns
agentmail._domainkey.yourdomain.com.  IN  TXT  "v=DKIM1; k=rsa; p=上一步输出的长字符串..."
```

**第三步：把私钥装到 AgentMail 身份上**

```bash
# 读私钥
PRIV=$(cat dkim_private.pem)

# 更新身份
curl -X PUT http://localhost:8081/v1/identities/kefu@yourdomain.com \
  -H "Authorization: Bearer *** \
  -d "{\"sign_key\":\"$PRIV\"}"
```

> 注意：`agentmail._domainkey` 中的 `agentmail` 是选择器（selector），可以改成任意名字（如 `default`、`mail`），关键是 AgentMail 的签名代码里要匹配。默认选择器是 `agentmail`。

#### 4. DMARC — 告诉收件方：如果 SPF/DKIM 校验失败怎么处理

```dns
_dmarc.yourdomain.com.  IN  TXT  "v=DMARC1; p=quarantine; rua=mailto:kefu@yourdomain.com"
```

参数说明：
- `p=quarantine` — 校验失败的邮件标记为垃圾（推荐）
- `p=reject` — 校验失败的邮件直接拒收（严格，适合稳定运营后启用）
- `p=none` — 只报告不处理（先设为 none 观察一段时间）
- `rua=mailto:...` — 收件方把检测报告发到这个邮箱

#### 验证全部生效

```bash
# 查询权威 DNS 验证
dig MX yourdomain.com +short
dig TXT yourdomain.com +short | grep spf
dig TXT agentmail._domainkey.yourdomain.com +short
dig TXT _dmarc.yourdomain.com +short
```

用在线工具检测邮件评分：https://www.mail-tester.com/

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
