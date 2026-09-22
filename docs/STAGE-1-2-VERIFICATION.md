# 阶段 1、2 验证记录

日期：2026-09-22

## 自动检查

- `go test -race ./...`：通过。
- `go vet ./...`：通过。
- Windows x64、macOS ARM64、Linux x64、Linux ARM64：Docker 交叉编译通过。
- 配置、身份、设备上限、协议分段读写、发现签名、配对篡改、配对过期、信任篡改、信任撤销、错误目标和未配对 TLS：均有自动测试。
- 集成测试完成发现解码、双方短码、信任落盘、真实 TCP 双向 TLS 和协议帧往返。

## 双容器配对

- 主电脑设备 ID：`fppu2tpt3ihvgnugygfwnkob4li7p4l2comauxoqw73eh6a4kwyq`
- 子电脑设备 ID：`7mwsqt4gqhrco3dc7b7mw7fxp2eeosee6vchnafvclodxq4k3gga`
- 双方显示配对码：`293269`
- 主电脑只保存子电脑证书，子电脑只保存主电脑证书。
- 两个容器退出状态均为 0。

以上身份和配对码仅属于一次性自动化验证，测试私钥在验证后删除。
