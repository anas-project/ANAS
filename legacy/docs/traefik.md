##概念

`Traefik`从1.x升级到2.x，基础概念已经大升级。1.x中的`Frontend``Backends`都已经被废弃了，现在`Traefik` 在结构上大致分为四级。`EntryPoints`（入口点），`Routers`（路由），`Services`（服务），`Providers`（提供者），在`Routers`中还可以使用`Middlewares`（中间件）来更改请求。

2.x采用模块化的结构，也支持了一些以前不能提供的功能，比如多域名自动在`Let's Encrypt`获取`HTTPS`证书。一些以前配置起来很麻烦的功能，也都变得很清晰，比如HTTP身份验证，添加Path前缀，现在他们分别通过`BasicAuth`和`AddPrefix`这两个中间件来实现。

配置文件格式从仅支持`TOML`，扩展到支持`TOML` 和 `YAML`。

###请求在`Traefik`中的流转
一个请求进入`Traefik`后，先会根据请求的`端口`进入不同`EntryPoints`（入口点）中。可以在`EntryPoints`中配置监听的端口，比如`80`--HTTP端口，`443`--HTTPS端口，或者一些TCP服务的端口，也可以先对请求进行超时设置等。

接下来请求会到达`Routers`（路由器），`Routers`和后端开发中的路由器概念很像，`HTTP`协议通过识别`URL`，`TCP`通过`端口`和`SNI`来决定怎么处理请求。把`HTTPS`请求转换成后端的`HTTP`，也是在这层实现的。`Routers`是把`EntryPoints`和后端的`Services`连在一起，我们配置`Traefik`主要是在`Routers`上。

通过`Routers`的过滤后，请求被分发到相应的`Services`（服务）中。`Services`可以理解为一组「后端服务」的集合，这个「后端服务」可以是另一个`Services`或者`Providers`（提供者）。`Providers`可以是一个`File Provider`配置文件，直接配置转发URL，也可以是一个`Docker Container`，直接对接Docker中提供的服务。 `Services`可以对这组后端服务做`负载均衡`，添加`Headers`，心跳检查等。

###TCP

`Traefik`在升级到2.0之后，增加了对TCP的支持，也就是可以直接使用`Traefik`转发TCP请求。像`MySQL`，`MongoDB`这些服务，通过`Traefik`统一暴露对外端口。

###配置

`Traefik`的配置分为两种，

####静态配置：

静态配置是配置在启动的时候就确定，之后也不会动态更新的设置。比如Log的保存地址，证书的提供商，`EntryPoints`的端口，或者一些`Providers`的设置，比如`File Provider`的地址，或者`Docker Provider`中Docker守护进程的路径等等。
静态配置有三种方式：
	
1. 配置文件
2. 命令行
3. 环境变量传递

####动态配置

`Traefik`在不同的`Provider`中提取相应的动态配置。比如在`Docker Provider`里，把配置写在Docker容器的`Label`中，`Traefik`通过Docker的守护进程，监测容器Label的变化，实时载入配置。`Traefik`也可以监控定义在静态配置中的`File Provider`文件，通过系统的文件修改通知API，实时更新配置。

我们一般在动态配置中配置：

1. `Routers`
2. `Services`
3. `Middlewares`

##在`Docker`中使用`Traefik`

在此项目中，`Traefik`作为反向代理的基础设施，在`Docker`中使用`Traefik`。对于每个服务，在容器的`Labels`中定义配置，即插即用。

在`Docker labels`中的可配置项[文档](https://docs.traefik.io/reference/dynamic-configuration/docker/)。

##TLS(HTTPS)

TLS是一种通讯加密技术，可有效防止在请求服务器时，由于线路不安全被窃听。如果我们在公网部署服务，使用TLS加密请求就非常有必要了。

TLS证书是由CA（数字证书认证机构）颁发的，我们可以主动向CA申请证书，也可以通过一些自动API，获取证书。

`Traefik`支持[`ACMEv2协议`](https://tools.ietf.org/html/rfc8555)获取并更新证书。ACME是Automated Certificate Management Environment（自动化证书管理环境）的缩写。ACME提供了向CA申请证书的通用协议，[支持ACME的CA](https://en.wikipedia.org/wiki/Automated_Certificate_Management_Environment#CAs_%26_PKIs_that_offer_ACME_certificates)。

在提供ACME协议的CA里，`Let's Encrypt`是免费的，也是`Traefik`的默认CA。我们接下来通过`Let's Encrypt`自动获取并更新TLS证书。这里要特别感谢`Let's Encrypt`服务，让我们可以免费、便捷的获取TLS证书。

在通过ACMEv2协议请求证书时，需要向CA证明你对申请的URL有所有权。证明所有权有三种方式

1. tlsChallenge(TLS-ALPN-01)
2. httpChallenge(HTTP-01)
3. dnsChallenge(DNS-01)

`tlsChallenge`和`httpChallenge`只能申请指定URL的证书，而且这两种验证要求服务在特定端口部署（443和80），众所周知，在大陆这两个端口一般是被封闭的。所以我们只能使用`dnsChallenge`，通过DNS服务商的API完成所有权验证。使用`dnsChallenge`还有一个好处，就是申请的域名支持通配符，通配符证书可以一次性支持多个子域名。[`Traefik`支持的DNS服务商列表](https://docs.traefik.io/https/acme/#providers)。

在配置过TLS证书后，后端`Service`是HTTP的，请求就为HTTPS，如果是TCP，客户端在访问该服务时，就要加一层`TLS Client`。

## docker-compose.yml 解析

### command:

* `--entrypoints.https.address=:443`: 在容器内导出https端口443，具体导出端口为下方`ports`中转出端口
* `--providers.docker=true`: 开启docker provider
* `- --providers.docker.exposedbydefault=false`: 不主动导出`docker container`作为provider，只有标记"traefik.enable=true"的`container`导出
* `--providers.docker.network=traefik`
*	`--api.dashboard=true`: 打开Traefik的dashboard，可以查看导出的`container`
* `--certificatesResolvers.cert.acme.dnschallenge=true`: 使用DNS challenge作为获取TLS证书的认证方式
* `--certificatesResolvers.cert.acme.dnschallenge.provider=${HTTPS_DNSCHALLENGE_PROVIDER}`: 使用环境变量`HTTPS_DNSCHALLENGE_PROVIDER`作为TLS DNS provider的提供商
* `--certificatesResolvers.cert.acme.email=${MY_EMAIL}`: 使用环境变量`MY_EMAIL`作为TLS申请时，提交的邮件地址
* `- --certificatesResolvers.cert.acme.storage=/acme/acme.json`: 在docker中证书的存储地址

### volumes:

* `- "/var/run/docker.sock:/var/run/docker.sock:ro"`: 挂载docker sock，traefik通过这个sock监控container的变化
* `- ${DATA_PATH}/acme:/acme`: 存储通过let's encrypt申请的证书

### ports:

* `- "9000:443"`: 把 9000 换成期望的端口

### labels:

* `- "traefik.http.routers.api.rule=Host(`traefik.${BASE_DOMAIN}`)"`: Traefik Dashboard地址
* `- "traefik.http.routers.api.service=api@internal"`: 在开启dashboard开关后，会自动生成`api@internal`的service
* `- "traefik.http.routers.api.middlewares=auth"`: 添加dashboard的Basic Auth验证
* `- "traefik.http.middlewares.auth.basicauth.users=${BASICAUTH_HTPASSWD}"`: 使用`echo $(htpasswd -nb [admin] [password]) | sed -e s/\\$/\\$\\$/g`生成用户名密码，用户名为**admin**，密码为**password**（务必修改）。密码建议由`openssl rand -base64 12`生成
* `- "traefik.enable=true"`: 在此container开启traefik
* `- "traefik.docker.network=traefik"`: 定义使用的docker网络

### networks:
```
  default:
    driver: bridge
    name: traefik
```
traefik在docker中生成的网络名称`traefik`，其他container需要接入这个网络才能代理。

参考文献：

1. [Traefik docs](https://docs.traefik.io/)
2. [Traefik翻译文档](https://www.qikqiak.com/traefik-book/) 
3. [ACME协议-RFC 8555](https://tools.ietf.org/html/rfc8555)




