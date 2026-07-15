Traefik
=====

`Traefik`是本项目的基础，任何通过TCP(HTTP)访问的服务都会通过Traefik反向代理。

配置
----------------

### 依赖的模块
无
### 需要的环境变量

- `LEGO_EMAIL`: 获取证书时，提交给Let's Encrypt的邮件，用于获取证书相关提醒
  - 如果为空，使用`core`的`Email`代替
- `LEGO_DNS_PROVIDER`: DNS提供者，LEGO支持市面上大部分DNS供应商的API，[支持列表](https://go-acme.github.io/lego/dns/)
  - 需要同时设置相应DNS供应商需要的环境变量定义，相应环境变量不会被检查，但是在docker-compose 运行时会出错
