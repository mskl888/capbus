# 参与贡献

感谢改进 capbus。项目优先保护用户已有配置，并保持每项改动可见、可验证、可撤销。

## 开始之前

- Bug 请附复现步骤；功能建议请先开 Issue 说明使用场景。
- 涉及架构、配置格式或安全边界的改动，请先讨论再实现。
- 不提交真实凭据、私人配置、用户数据或生成的临时目录。

## 本地开发

需要 Go 1.22 或更高版本。

```bash
git clone https://github.com/mskl888/capbus.git
cd capbus
gofmt -w <修改过的 Go 文件>
go vet ./...
go test ./... -count=1
go build ./...
```

CI 会在 Linux、macOS 和 Windows 上检查 gofmt、vet、测试和构建。

## 改动要求

1. 每个 PR 聚焦一个问题，说明动机、行为和验证方法。
2. 新行为或 Bug 修复先添加会失败的回归测试，再做最小实现。
3. 测试通过公开边界验证行为，不绑定私有实现细节。
4. 非 capbus 写入的 Agent 配置必须保持不变；失败路径不能留下半份配置。
5. 用户可见的行为、限制和命令变化必须同步更新文档。
6. 不改变共享后端的实验性边界，不把通知广播描述成完整会话隔离。

## DSH 与真实 Agent 验证

测试必须使用临时 `HOME`、`CAPBUS_HOME` 和 `DSH_HOME`。不要停止正式服务，不要修改正式 DSH profile，也不要为了测试卸载用户已有插件。

真实验收至少覆盖安装、状态读取、重启、撤销和清理；无法执行真实验收时，在 PR 中明确标注“未验证”，不要写成已通过。

## 提交信息

使用简短、具体的英文祈使句，例如：

```text
inject: require an explicit target for multiple profiles
docs: add private security reporting instructions
```
