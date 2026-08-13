CREATE DATABASE IF NOT EXISTS `{{LLNG_DB_NAME}}` CHARACTER SET utf8;
use {{LLNG_DB_NAME}}

-- config
CREATE TABLE IF NOT EXISTS lmConfig (
    cfgNum int not null primary key,
    data longtext
) DEFAULT CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;

-- Notification
CREATE TABLE IF NOT EXISTS notifications (
  date datetime NOT NULL,
  uid varchar(255) NOT NULL,
  ref varchar(255) NOT NULL,
  cond varchar(255) DEFAULT NULL,
  xml longblob NOT NULL,
  done datetime DEFAULT NULL,
  PRIMARY KEY (date, uid,ref)
) DEFAULT CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;

-- SESSION
CREATE TABLE IF NOT EXISTS sessions (
    id varchar(64) not null primary key,
    a_session text,
    _whatToTrace varchar(64),
    _session_kind varchar(15),
    ipAddr varchar(64),
    _utime bigint,
    _httpSessionType varchar(64),
    user varchar(64)
) DEFAULT CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;
CREATE INDEX IF NOT EXISTS i_s__whatToTrace ON sessions (_whatToTrace);
CREATE INDEX IF NOT EXISTS i_s__session_kind ON sessions (_session_kind);
CREATE INDEX IF NOT EXISTS i_s__utime ON sessions (_utime);
CREATE INDEX IF NOT EXISTS i_s_ipAddr ON sessions (ipAddr);
CREATE INDEX IF NOT EXISTS i_s__httpSessionType ON sessions (_httpSessionType);
CREATE INDEX IF NOT EXISTS i_s_user ON sessions (user);

CREATE TABLE IF NOT EXISTS psessions (
    id varchar(64) not null primary key,
    a_session text,
    _session_kind varchar(15),
    _httpSessionType varchar(64),
    _whatToTrace varchar(64),
    ipAddr varchar(64),
    _session_uid varchar(64)
)  DEFAULT CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;
CREATE INDEX IF NOT EXISTS i_p__session_kind ON psessions (_session_kind);
CREATE INDEX IF NOT EXISTS i_p__httpSessionType ON psessions (_httpSessionType);
CREATE INDEX IF NOT EXISTS i_p__session_uid ON psessions (_session_uid);
CREATE INDEX IF NOT EXISTS i_p_ipAddr ON psessions (ipAddr);
CREATE INDEX IF NOT EXISTS i_p__whatToTrace ON psessions (_whatToTrace);

CREATE TABLE IF NOT EXISTS samlsessions (
    id varchar(64) not null primary key,
    a_session text,
    _session_kind varchar(15),
    _utime bigint,
    ProxyID varchar(64),
    _nameID varchar(255),
    _assert_id varchar(64),
    _art_id varchar(64),
    _saml_id varchar(64)
)  DEFAULT CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;
CREATE INDEX IF NOT EXISTS i_a__session_kind ON samlsessions (_session_kind);
CREATE INDEX IF NOT EXISTS i_a__utime ON samlsessions (_utime);
CREATE INDEX IF NOT EXISTS i_a_ProxyID ON samlsessions (ProxyID);
CREATE INDEX IF NOT EXISTS i_a__nameID ON samlsessions (_nameID);
CREATE INDEX IF NOT EXISTS i_a__assert_id ON samlsessions (_assert_id);
CREATE INDEX IF NOT EXISTS i_a__art_id ON samlsessions (_art_id);
CREATE INDEX IF NOT EXISTS i_a__saml_id ON samlsessions (_saml_id);

CREATE TABLE IF NOT EXISTS oidcsessions (
    id varchar(64) not null primary key,
    a_session text,
    _session_kind varchar(15),
    _utime bigint
)  DEFAULT CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;
CREATE INDEX IF NOT EXISTS i_o__session_kind ON oidcsessions (_session_kind);
CREATE INDEX IF NOT EXISTS i_o__utime ON oidcsessions (_utime);


CREATE TABLE IF NOT EXISTS cassessions (
    id varchar(64) not null primary key,
    a_session text,
    _session_kind varchar(15),
    _utime bigint,
    _cas_id varchar(128),
    pgtIou varchar(128)
) DEFAULT CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;
CREATE INDEX IF NOT EXISTS i_c__session_kind ON cassessions (_session_kind);
CREATE INDEX IF NOT EXISTS i_c__utime        ON cassessions (_utime);
CREATE INDEX IF NOT EXISTS i_c__cas_id       ON cassessions (_cas_id);
CREATE INDEX IF NOT EXISTS i_c_pgtIou        ON cassessions (pgtIou);