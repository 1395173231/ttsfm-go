# TTSFM - Text-to-Speech for Masses

OpenAI 兼容的文本转语音服务，使用 Go 语言实现。

## 功能特性

- 🎯 OpenAI TTS API 兼容 (`/v1/audio/speech`)
- 🔊 支持多种语音：alloy, ash, ballad, coral, echo, fable, nova, onyx, sage, shimmer, verse
- 🎵 支持多种格式：mp3, wav, opus, aac, flac, pcm
- 📝 自动分割和合并长文本
- 🔐 可选的 API 密钥认证
- 🚦 可选的速率限制
- 🐳 Docker 支持

## 快速开始

### 直接运行

```bash
go run cmd/main.go
```

### Docker

```bash
# 构建并运行
docker-compose up -d

# 查看日志
docker-compose logs -f
```

### 使用示例

```bash
# 生成语音
curl -X POST http://localhost:8080/v1/audio/speech \
  -H "Content-Type: application/json" \
  -d '{
    "input": "Hello, world!",
    "voice": "alloy",
    "response_format": "mp3"
  }' \
  --output output.mp3

# 健康检查
curl http://localhost:8080/health
```

## API 端点

| 端点 | 方法 | 描述 |
|------|------|------|
| `/v1/audio/speech` | POST | 生成语音（OpenAI 兼容） |
| `/v1/voices` | GET | 获取可用语音列表 |
| `/v1/formats` | GET | 获取支持的格式列表 |
| `/health` | GET | 健康检查 |

## 配置

通过环境变量或命令行参数配置：

| 参数 | 环境变量 | 默认值 | 描述 |
|------|----------|--------|------|
| `-host` | `TTSFM_HOST` | `0.0.0.0` | 监听地址 |
| `-port` | `TTSFM_PORT` | `8080` | 监听端口 |
| `-enable-auth` | `TTSFM_ENABLE_AUTH` | `false` | 启用认证 |
| `-api-keys` | `TTSFM_API_KEYS` | - | API 密钥列表 |
| `-timeout` | `TTSFM_TIMEOUT` | `60s` | 请求超时 |

## License

MIT