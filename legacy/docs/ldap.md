
## 简介

`LDAP`全称是**Lightweight Directory Access Protocol（轻量目录访问协议）**，是一种树状结构的数据库，现在被主要用来做通用用户身份验证服务。也就是可以实现在LDAP创建一个用户，该用户可以在其他所有支持LDAP的服务上登陆（如`Nextcloud`，`GitLab`等）；在LDAP上删除该用户后，该用户失去所有平台使用权限。

LDAP是企业级账户管理的标准协议，当前版本是3，大部分企业级开源软件都支持LDAP协议。目前比较流行的LDAP开源实现是[OpenLDAP](https://www.openldap.org/)，我们接下来也是用OpenLDAP作为我们用户验证服务器。

LDAP是由一系列IETF发布的RFC构成的协议，关于LDAP的历史和RFC，可以参照这篇文章[LDAP介绍（一）](https://yq.aliyun.com/articles/472398)，[RFCs](https://zh.wikipedia.org/wiki/%E8%BD%BB%E5%9E%8B%E7%9B%AE%E5%BD%95%E8%AE%BF%E9%97%AE%E5%8D%8F%E8%AE%AE#RFC)。

## 概念

借用[维基百科](https://zh.wikipedia.org/wiki/%E8%BD%BB%E5%9E%8B%E7%9B%AE%E5%BD%95%E8%AE%BF%E9%97%AE%E5%8D%8F%E8%AE%AE)的示意图。

```
         dc=org,dc=wikipedia
       /            \
 ou=people          ou=groups
    /     \
cn=Musk    cn=Hailong
```

示例中的每个节点是一个`条目（Entry）`，每个条目包含一个或多个`ObjectClass（对象类）`，每个对象类包含一些可选或者必选的`属性（Attribute）`，我们在RFC中规定了很多预先定义的规范，这就是`模式（Schema）`。

来看这几个概念：

### 条目（Entry）

示例中，最上面的`dc=org,dc=wikipedia`是`root`(根)，也是个特殊的Entry，下面的每一个节点就是一个Entry。Entry是在LDAP中用来存储信息的单元。

Entry的组成：

1. 有一个可以在LDAP中唯一识别的`DN`
2. 一个或多个`ObjectClass`
3. `ObjectClass`包含一个或多个`Attribute`

### ObjectClass（对象类）

`ObjectClass`和`条目`的关系可以理解为编程中的`class`和`object`，`ObjectClass`定义一些属性，条目对`ObjectClass`定义的属性赋值。

`ObjectClass`有类型的区分，类型包括`结构型（Structural）`，`辅助型（Auxiliary） `，`抽象型（Abstract）`，对应的概念类似`类`，`接口`，`抽象类`。一个条目有且只有一个`结构型`的`ObjectClass`，用来定义条目的主要属性；可以有零个或多个`辅助型`的`ObjectClass`，定义附加属性。条目不能有`抽象型`的`ObjectClass`，`抽象型`的`ObjectClass`只能被`ObjectClass`继承。

所有结构型`ObjectClass`都直接或间接的继承了`top`这个抽象型的`ObjectClass`，`top`的参考：[top](https://ldapwiki.com/wiki/2.5.6.0)

一个`ObjectClass`的定义：

```
objectclass	( 2.16.840.1.113730.3.2.2
    NAME 'inetOrgPerson'
	DESC 'RFC2798: Internet Organizational Person'
    SUP organizationalPerson
    STRUCTURAL
	MAY (
		audio $ businessCategory $ carLicense $ departmentNumber $
		displayName $ employeeNumber $ employeeType $ givenName $
		homePhone $ homePostalAddress $ initials $ jpegPhoto $
		labeledURI $ mail $ manager $ mobile $ o $ pager $
		photo $ roomNumber $ secretary $ uid $ userCertificate $
		x500uniqueIdentifier $ preferredLanguage $
		userSMIMECertificate $ userPKCS12 )
	)
```

参考： [ObjectClass Types](https://ldapwiki.com/wiki/ObjectClass%20Types)，[ObjectClass](https://ldapwiki.com/wiki/ObjectClass)，[ldap objectclass](https://www.cnblogs.com/pycode/p/9495808.html)

### 属性（Attribute）

属性是ObjectClass的组成部分。我们的名字、生日、邮件地址都可以是个属性。ObjectClass中的属性可以是可选和必选。

属性的定义称为`AttributeTypes`，也就是如何描述一条属性。一般属性常用描述有

1. `NAME`：可读名称
2. `DESC`：介绍
3. `EQUALITY` `SUBSTR` `ORDERING`：匹配规则，参考[匹配规则](http://ldapwiki.com/wiki/MatchingRule)
4. `SYNTAX`：直译过来是语法，实际上是描述属性的类型，参考[类型列表](http://ldapwiki.com/wiki/LDAPSyntaxes)。
5. `OID`：Object Identifier 对象标识符，在下一节介绍

一个属性的描述，这个属性正是上面`ObjectClass``inetOrgPerson`的一个可选属性：

```
attributetype ( 2.16.840.1.113730.3.1.1
	NAME 'carLicense'
	DESC 'RFC2798: vehicle license or registration plate'
	EQUALITY caseIgnoreMatch
	SUBSTR caseIgnoreSubstringsMatch
	SYNTAX 1.3.6.1.4.1.1466.115.121.1.15 )
```


具体描述，参照：[AttributeTypes定义](http://ldapwiki.com/wiki/AttributeTypes)

下面是一些常用的属性：

1. DC (Domain Component) 域组件，每一个DC是域名的一部分，例如上面的`dc=wikipedia,dc=org`组合起来就是域名的全部`wikipedia.org`，我们也可以增加更多的DC，如`dc=zh,dc=wikipedia,dc=org`。
2. CN (Common Name) 名称，能描述对象的名字。
3. OU (Organizational Unit) 组织单元名称，每一个OU节点下面，应该为一组数据。比如`OU=groups`，下面存所有部门名称；比如`OU=groups`，下面存所有部门名称；`OU=people`，下面存所有的用户信息。
4. DN (Distinguished Name) 识别名，DN是从Root开始到Entry所在节点的全路径，DN对于当前系统来说是唯一的。比如示例中的`cn=Musk`条目的DN是`cn=Musk, ou=people, dc=org, dc=wikipedia`。
5. RDN（Relative DN）相对时别名，类似Linux中的相对路径，可配置用`cn=Musk`即为该Entry的RDN。

参考：[OpenLDAP 属性](https://wiki.shileizcc.com/confluence/pages/viewpage.action?pageId=39223517)

### 对象标识符（OID）

`OID`是国际电信联盟和国际标准化组织定义的一套能全球唯一识别一个对象的识别码。在LDAP里，每个属性都有一个唯一的`OID`标识。通过这个唯一的`OID`，就很容易在晚上找到这个属性定义的内容。

`OID`是由数字加`.`组成。和LDAP一样，`OID`也是树结构，每一个`.`后面是该节点的子节点。比如上面`carLicense`的`OID`是`2.16.840.1.113730.3.1.1`，他的父`OID`是`2.16.840.1.113730.3.1`，如果需要他还可以建立子节点`2.16.840.1.113730.3.1.1.1`。

两个查询`OID`网站：

1. [oid-info.com](http://www.oid-info.com/)
2. [oidref.com](https://oidref.com/)

`OID`的申请是开放的，任何一个组织都可以申请自己的`OID`，[组织申请地址](http://pen.iana.org/pen/PenApplication.page)。申请通过之后，我们就可以在这个前缀后面添加我们自己的`OID`。如果需要使用`OID`，一定不要自己定义，这样就失去`OID`的意义了。

参考：[Wikipedia: Object identifier](https://en.wikipedia.org/wiki/Object_identifier)

### 模式（Schema）

`Schema`是一系列`ObjectClass`和`Attribute`的合集，它定义了一些LDAP标准的使用方式，比如创建一个部门，一个员工。

Schema可以自定义，不过如果没有特殊需求，服务器提供的Schema足够使用。

参考：[OpenLDAP Schema 概念](https://wiki.shileizcc.com/confluence/pages/viewpage.action?pageId=39223501)

## OpenLDAP

`OpenLDAP`是常用的LDAP的开源实现，除了`OpenLDAP`，在Windows Server上有自带的`Active Directory`，macOS有`Open Directory`。

LDAP Data Interchange Format，简称LDIF，是与LDAP服务器进行通讯的标准交换格式。LDIF文件基本是可读的，关于格式标准参考[LDAP Data Interchange Format](https://ldapwiki.com/wiki/LDAP%20Data%20Interchange%20Format)

https://www.zytrax.com/books/ldap/ch2/index.html#history




http://www.openldap.org/doc/
https://segmentfault.com/a/1190000014683418
https://ldapwiki.com/
https://www.zytrax.com/books/ldap/