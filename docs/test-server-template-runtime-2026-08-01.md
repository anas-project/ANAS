# 容器内配置生成远端回归报告

测试日期：2026-08-01 至 2026-08-02

测试服务器：`whl@ln.hlong.wang:2200`

## 结论

ERB 移除和容器内配置生成方案已在隔离 Docker daemon 中完成全新部署、运行时配置检查、协议探针和整栈重启测试。最终 8 个容器全部稳定运行，带健康检查的服务全部 healthy，重启计数均为 0；整栈重启后的同一组探针再次通过。

测试完成后已删除隔离容器和网络，停止专用 daemon，并删除专用 Docker data-root、exec-root、socket 和 network namespace。宿主原有 6 个业务容器的 ID 和运行状态与测试前完全一致。

## 隔离环境

- 代码目录：`/home/whl/anas-template-runtime-test`
- workspace：`/home/whl/anas-template-runtime-test/.anas-test/runtime/template-runtime`
- Docker socket：`/run/anas-template-docker.sock`
- Docker data-root：`/data/anas-template-docker`
- network namespace：`anas-tmpl`
- 测试地址：`10.252.0.2/24`
- 容器、镜像和网络前缀：`anas_tmpl_`

测试模块为 Samba DC、Samba FS、MariaDB、MeshCentral、LAM 和 Eturnal，以及自动依赖的 Lego、Traefik。

## 验证结果

| 范围 | 结果 | 验证内容 |
| --- | --- | --- |
| 构建与编排 | 通过 | 远端 Go 测试、静态检查、render、8 份 Compose 配置解析、`anas apply --build` |
| 模板迁移 | 通过 | 发布目录无 `.erb`、`.j2`、`.j3`、`.tmpl`；运行时配置无未解析占位符 |
| Traefik | 通过 | 容器内生成 `/run/anas/cert.yml`，权限 0600，ping healthy，Dashboard 未认证返回 401 |
| Eturnal | 通过 | 容器内生成 `/run/anas/eturnal.yml`，权限 0600，`eturnalctl status`，TCP/UDP 3478 监听，停止耗时 2 秒 |
| MeshCentral | 通过 | 容器内生成合法 JSON，权限 0600，LDAP/MySQL 字段和 Node 依赖有效，HTTPS 返回 200 |
| LAM | 通过 | 使用镜像自带 PHP 生成配置，无 gettext 运行时依赖，保留字面量 `$INFO.userPasswordClearText$`，HTTPS 返回 200 |
| Samba DC | 通过 | `testparm`、`named-checkconf`、域状态以及 TCP 88/389/636 |
| Samba FS | 通过 | `testparm`、`wbinfo -t`，整栈重启后 trust 恢复 |
| MariaDB | 通过 | healthy，root 本地查询 `select 1` |
| 重启 | 通过 | 整栈 down/up 后所有生成配置、健康检查、AD trust、DNS 和 HTTPS 探针再次通过 |

## 实测发现并修正的问题

1. Traefik 官方镜像的二进制位于 `PATH`，不是 `/traefik`；入口脚本改为执行 `traefik`。
2. MeshCentral 启动时一次性 `ping` Traefik 会受启动顺序影响；改为带超时的 Docker DNS 解析重试。
3. LAM 基础镜像自带 PHP CLI；改用 PHP 的精确字符串替换，移除从 Debian Buster archive 安装 gettext 的构建步骤。
4. Eturnal 子镜像重设 `ENTRYPOINT` 后会丢失上游 `CMD`；显式恢复为直接执行 `run.sh`。
5. 入口脚本中导出的 `ETURNAL_ETC_DIR` 不会进入 Docker healthcheck exec；改为镜像级 `ENV`。
6. Eturnal 的 1001 个 UDP relay 端口使用 bridge `ports` 会生成大量代理/NAT 规则并导致启停缓慢；改为 host networking，并在专用测试 daemon 中关闭 userland proxy。
7. Samba FS 重启时重复删除 `/etc/machine-id` 会退出；改为幂等的 `rm -f`。

## 报告位置

- 最终 host-network 探针：`/home/whl/anas-template-runtime-test/test-env/reports/2026-08-01-template-runtime/host-network-probes.log`
- 整栈重启探针：`/home/whl/anas-template-runtime-test/test-env/reports/2026-08-01-template-runtime/restart-probes.log`
- 最终清理日志：`/home/whl/anas-template-runtime-test/test-env/reports/2026-08-01-template-runtime/cleanup-final.log`
