# Traefik 运维

Traefik 是 ANAS 的 HTTPS 入口。当前 Module 使用 Traefik `3.7.10`，依赖 `lego` 提供证书；Traefik 自身不运行 ACME challenge。

## 请求路径

```text
客户端 -> 主机 TRAEFIK_BASE_PORT -> Traefik https entrypoint
       -> Docker provider 或 file provider 路由 -> 目标服务
```

默认端口是 `9000`，容器内外使用同一端口。Dashboard 默认地址为：

```text
https://traefik.<base_domain>:9000
```

如果将 `base_port` 改为标准 HTTPS 端口 `443`，URL 可以省略端口。

## 配置

```yaml
modules:
  lego:
    config:
      dns_provider: cloudflare
  traefik:
    config:
      base_port: 9000
      domain_prefix: traefik

global:
  base_domain: nas.example.com
  email: admin@example.com
  default_service_root_password: replace-with-a-strong-password
  basicauth_user: admin

secrets:
  cloudflare_dns_api_token: replace-me
```

`lego` 通过 DNS-01 获取通配符证书，并将证书目录只读挂载给 Traefik。DNS 厂商凭据属于 Secret，不应提交到仓库。修改 `base_port` 或 `domain_prefix` 会重建 Traefik 容器及相关路由。

Dashboard 使用 BasicAuth。用户名来自 `global.basicauth_user`（默认 `admin`），兼容配置下密码来自 `global.default_service_root_password`。也可以在顶层 `env` 中提供 `BASICAUTH_PASSWD` 或完整的 `TRAEFIK_BASICAUTH_HTPASSWD`；后者属于高级兼容接口。

## 路由来源

同一 Traefik Docker 网络中的容器使用 Docker labels 声明路由。Traefik 只发现显式设置 `traefik.enable=true`、匹配当前 ANAS 实例 label 且接入当前 Traefik 网络的容器。

host 网络、Docker 外进程或只能按地址访问的服务使用 file provider。Module 通过以下环境变量发布路由：

```text
ANAS_TRAEFIK_ROUTE__<NAME>__RULE          必填，Traefik 规则表达式
ANAS_TRAEFIK_ROUTE__<NAME>__URL           必填，上游地址
ANAS_TRAEFIK_ROUTE__<NAME>__MIDDLEWARES   可选，逗号分隔
ANAS_TRAEFIK_ROUTE__<NAME>__ENTRYPOINTS   可选，默认 https
ANAS_TRAEFIK_ROUTE__<NAME>__TLS           可选，默认 true
```

这些变量是 Module 间契约，不建议用户在普通配置中手写。开发 Module 时，发布方还必须在 `config.exports` 中声明 `ANAS_TRAEFIK_ROUTE__*`。

## 安全边界

- Docker socket 以只读方式挂载，但仍具有很高的宿主机可见性；只允许受信任的 Traefik 镜像访问。
- 不要关闭 Dashboard 的 BasicAuth，也不要将真实密码写入公开配置示例。
- `DOCKER_SOCKET_PATH` 可通过顶层 `env` 指向兼容 socket；这是高级宿主机设置，不是 Module 参数。
- 防火墙只需放行实际使用的入口端口。默认 `9000` 不是 `80/443`。

## 排查

```bash
anas status -w /srv/anas
docker logs anas_traefik
```

按以下顺序检查：

1. 域名是否解析到 ANAS 主机，访问 URL 是否包含非标准端口；
2. `lego` 是否成功生成证书，证书目录是否已挂载；
3. 目标容器是否接入当前 Traefik 网络并带有实例 label；
4. file-provider 路由的上游地址是否能从 Traefik 容器访问；
5. 改端口后，防火墙与其他服务是否存在端口冲突。

通用网络检查见[网络运维](networking.md)，Module 参数见[配置结构参考](/reference/configuration)。
