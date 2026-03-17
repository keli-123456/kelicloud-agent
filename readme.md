# komari-agent

支持使用环境变量 / JSON配置文件来传入 agent 参数

详见 `cmd/flags/flags.go` 及 `cmd/root.go`

## 发布前校验

```bash
GOCACHE=$PWD/.gocache GOMODCACHE=$PWD/.gomodcache go test ./...
go build -trimpath -ldflags="-s -w -X github.com/komari-monitor/komari-agent/update.CurrentVersion=verify" -o komari-agent .
```

仓库里也提供了一个手动触发的 GitHub Actions 工作流 `Verify Runtime`，会执行同一套测试和构建校验。
