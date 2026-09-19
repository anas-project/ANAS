# Incus 镜像交付最终集成核对

日期：2026-09-19。范围限于 Incus 镜像 artifact、Runner supply 接线、Provider import、发布脚本与镜像供给文档；未修改 hostaction、jobexecutor、installer、API、UI、共享计划索引或共享 build release 脚本。

## 结果

已把既有 helper 串成 `build-once -> immutable artifact -> release bundle -> frozen resolution -> read-only supply -> Provider import -> metadata recheck` 的代码闭环。

- 发布侧 `incus-image-artifacts build` 仍只在独立发布构建机运行 distrobuilder，同 revision 已存在时复核并复用，缺失/损坏/recipe 变化时失败。
- `scripts/ci/incus-image-release-build.sh` 现在输出运行时直接消费的 `images/catalog.json` 与 `images/artifacts/anas/forgejo-runner/<revision>/<architecture>/<interface>/...`，不再产出与 Runner 查找路径脱节的 `exports/...` 布局。
- Core/Runner 使用 deployment 中冻结的 `ComputeImages`，不重新查询最新 catalog，不接受 floating URL/tag/alias，不从 artifact descriptor 自证 fingerprint。
- Runner 在 Provider `ensure` 前按 frozen catalog 引用收集 artifact，复制到私有临时目录并以 Docker Compose `--volume ...:ro` 挂入 provisioner；无本地 artifact 时不挂载 supply，Provider 在目标 daemon 缺镜像时 fail closed。
- Provider 只接受固定路径的 supply descriptor，导入 split artifact 后等待合法 Incus async operation，并再次 GET image metadata 校验 fingerprint、架构和 container/vm type。

## 本轮修复

- Runner artifact 打开/复制从仅检查大小，改为拒绝符号链接、特殊文件、可写文件、路径替换和大小漂移，并按 release part SHA-256 重新计算真实字节 digest。
- Runner descriptor 读取改为 nofollow 打开并读后复核文件身份，artifact 路径检查覆盖 providerDir 自身与中间目录的符号链接。
- Provider supply 读取增加固定 root 与中间目录权限/符号链接校验；part 打开前后仍保留 nofollow、单链接、只读、大小和替换复核。
- 双语 `docs/architecture/incus-image-supply.md` 记录当前已接线的 read-only supply 行为、发布 bundle 布局、安全边界与未验证项。

## 验证

已通过：

```text
GOCACHE=/private/tmp/anas-go-build go test ./internal/computeimage ./cmd/incus-image-artifacts ./cmd/compute-image-artifact
GOCACHE=/private/tmp/anas-go-build go test ./modules/incus/provisioner -run 'TestLoadImageSupplyRejectsHostileJSON|TestOpenSuppliedReadersRejectsUnsafeFiles|TestOpenSuppliedReadersRejectsUnsafeSupplyDirectories|TestCopyImagePartDetectsPathReplacementAfterOpen|TestImagePrunePlanKeepsCurrentPreviousAndRunning|TestLeaseValidationRejectsUnsafeInput'
GOCACHE=/private/tmp/anas-go-build GOOS=linux GOARCH=amd64 go test -c -o /private/tmp/anas-computeimage.test ./internal/computeimage
GOCACHE=/private/tmp/anas-go-build GOOS=linux GOARCH=amd64 go test -c -o /private/tmp/anas-incus-image-artifacts.test ./cmd/incus-image-artifacts
GOCACHE=/private/tmp/anas-go-build GOOS=linux GOARCH=amd64 go test -c -o /private/tmp/anas-compute-image-artifact.test ./cmd/compute-image-artifact
GOCACHE=/private/tmp/anas-go-build GOOS=linux GOARCH=amd64 go test -c -o /private/tmp/anas-incus-provisioner.test ./modules/incus/provisioner
```

受阻：

- `GOCACHE=/private/tmp/anas-go-build go test ./internal/runner -run 'TestComputeImageSupply|TestComputeSupply|TestCollectComputeSupply|TestComputeImagePrune'` 未进入本轮测试，因范围外 `internal/hostaction/incus_parameters.go` 编译失败：`undefined: confirmationDigestPattern`。
- 带 fake daemon 的 provisioner 测试仍被当前 macOS sandbox 禁止 `httptest` 监听：`listen tcp6 [::1]:0: bind: operation not permitted`。未提权、未放宽 sandbox。
- `GOOS=linux GOARCH=amd64 go test ... -run '^$'` 不能在 macOS 直接运行 Linux 测试二进制，已改用 `go test -c` 验证 Linux 编译。

## 未验证

- 未运行真实 distrobuilder bake；没有生成或伪造新的 catalog fingerprint。
- 未验证真实 Incus multipart import、guest 启动、VM/KVM、系统容器隔离、Forgejo one-job 行为等价。
- 未执行 Docker、SSH、root 或真实宿主操作。
- Prune 仍停留在 dry-run 纯计划；没有删除真实镜像，也没有接入宿主二段确认动作。
