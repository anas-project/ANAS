
## 简介

`Samba`是一套Linux/Unix上开源的和Windows互操作的套件，主要有以下几个功能，
1. 基于[SMB协议](https://zh.wikipedia.org/wiki/%E4%BC%BA%E6%9C%8D%E5%99%A8%E8%A8%8A%E6%81%AF%E5%8D%80%E5%A1%8A)文件和打印机共享的服务器端和客户端。
2. 作为`Domain member`接入微软的`Active Directory（AD）`，让`AD`管理Linux上的用户和群组。
3. 全功能的`Active Directory` 的域控制器（`Domain Controller`），兼容`windows server 2008 r2`协议，完全可以替代windows server，管理域内计算机。

使用过windows server的朋友，对于`Active Directory（AD）`一定不陌生。AD作为`Windows Server`的目录管理服务，掌握着域（`Domain`）内的所有资源，包括用户，计算机，组，软件，权限等。想象这样一个场景，我们每次重装系统都要填写用户名密码，还要分配一个默认的管理员权限，之后要装软件，调整系统设置等等。如果我们要为家里爸爸、妈妈、姐姐和哥哥安装系统，那每次都要重复这些步骤。AD的作用就是让我们在一台服务器上统一管理这些权限和配置，每次有新的电脑加入，我们可以通过AD的管理员账户，把计算机加入域，然后设置域内的哪个用户可以访问这台电脑，有什么权限，用户在AD上的配置信息（组策略）也会自动部署到这台电脑上。我们也可以允许爸爸妈妈作为普通用户访问这台电脑，哥哥有这台电脑的管理员权限，他们登录之后都会自动部署各自的组策略。

当然，我们也可以把AD集成在一些软件里，如`nextcloud`，这样家人就可以以相同的用户名密码登录相应的服务。

域（`Domain`）也称网域。域是一个范围，可以是一个家庭，也可以是个小型的公司，如果公司组织结构庞大复杂，也可以针对不同分公司，或者部门划分子域。域通常作为一个管理单位，我们可以在域内指定不同权限的管理员。

域控制器`Domain Controller（DC）`，顾名思义就是用来提供域服务的服务器。域控制器可以在域内提供用户的身份认证，存储用户账户信息，执行域组策略等服务。`Windows Server`可以提供DC的集群功能，也就是在网内部署多个DC，实现故障转移。`Samba`也可以提供相同的功能，即可以加入`Windows Server`的集群，也可以组建自己的集群。

在`AD`中，我们需要给每个域起个名字，称为域名。这个域名和我们平时访问网站的域名类似，也是通过DNS解析的。域名最好命名为我们公网上拥有的域名。如果我拥有`example.com`这个域名，我AD的域名可以是`example.com`这样的顶级域名，或者`corp.example.com`这样的子域名。

`AD`是要依赖内网的DNS工作的。`Samba`支持两种DNS解析方式，Samba内置的DNS，或者使用开源的`BIND`DNS服务器。正常情况下Samba内置DNS已经提供了足够可以让`Samba DC`运行起来的功能。`BIND`则可以提供更多的额外功能。

### AD的最佳实践

`AD`作为一种`LDAP`服务的实现，同样也提供了非常灵活的信息组织形式。参考Dan Holme的最佳实践[<sup>1</sup>](#refer-anchor-1)[<sup>2</sup>](#refer-anchor-2)[<sup>3</sup>](#refer-anchor-3)，我总结了一套适合小型企业和家庭的`AD`使用方法。这里只讨论在一台`DC`上的用户，计算机，群组的管理方法，有关组策略和集群方面，请看`Dan Holme`的视频。

在`AD`中管理的资源有，用户（User），计算机（Computer），组（Group）

#### 管理员

1. 根据最佳实践，我们应该按照不同权限划分不同管理员，但是因为我们的信息相对来说没有那么复杂，所以我建议只用一个管理员账户即可。
2. 在我们日常使用中，非必要的情况下，尽量避免使用管理员账户，而是应该单独建立一个日常使用的普通账户。
3. 修改管理员的默认名字（Administrator）。



#### 用户

##### 登录名

登录名必须在域内唯一，`Windows 2000`以前使用`sAMAccountName`作为登录用户名，


##### 密码



### User ID

在Linux上集成`AD`，需要把`AD`上的用户和组对应到Linux的`User ID`/`Group ID`上。ANAS默认使用`tdb` 

https://www.samba.org/samba/docs/current/man-html/smb.conf.5.html#idm410


在使用`ad ID mapping`的时候，如果要将`Administrator`用户映射为`root`用户，不要设置`Administrator`用户的`uidNumber`，否则会覆盖`root`用户为`0`的`UID`

### 参考
<div id="refer-anchor-1">

1. [Active Directory Best Practices - Ten Years Later](https://www.youtube.com/watch?v=_Q-rLcBKJaw)
</div>
<div id="refer-anchor-2">

2. [Role-Based Management Extreme Makeover](https://www.youtube.com/watch?v=IKzokBgCp60&t=1161s)
</div>
<div id="refer-anchor-3">

3. [Active Directory Best Practices - Ten Years Later - PDF](http://download.microsoft.com/download/e/a/7/ea75457b-65d0-481c-b53b-d7ca2ae7ee08/s2b%20-%209.pdf)
</div>

