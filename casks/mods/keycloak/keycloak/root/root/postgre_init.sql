-- create database if not exist
SELECT 'CREATE DATABASE "{{LLNG_DB_NAME}}"'
WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = '{{LLNG_DB_NAME}}')\gexec

\c "{{LLNG_DB_NAME}}"

-- config
CREATE TABLE IF NOT EXISTS lmConfig (
    cfgNum int not null primary key,
    data text
);

-- Notification
CREATE TABLE IF NOT EXISTS notifications (
  date timestamp NOT NULL,
  uid varchar(255) NOT NULL,
  ref varchar(255) NOT NULL,
  cond varchar(255) DEFAULT NULL,
  xml bytea NOT NULL,
  done timestamp DEFAULT NULL,
  PRIMARY KEY (date, uid,ref)
);

-- SESSION
CREATE UNLOGGED TABLE IF NOT EXISTS sessions (
    id varchar(64) not null primary key,
    a_session jsonb
);

CREATE INDEX IF NOT EXISTS i_s__whatToTrace     ON sessions ((a_session ->> '_whatToTrace'));
CREATE INDEX IF NOT EXISTS i_s__session_kind    ON sessions ((a_session ->> '_session_kind'));
CREATE INDEX IF NOT EXISTS i_s__utime           ON sessions ((cast (a_session ->> '_utime' as bigint)));
CREATE INDEX IF NOT EXISTS i_s_ipAddr           ON sessions ((a_session ->> 'ipAddr'));
CREATE INDEX IF NOT EXISTS i_s__httpSessionType ON sessions ((a_session ->> '_httpSessionType'));
CREATE INDEX IF NOT EXISTS i_s_user             ON sessions ((a_session ->> 'user'));


CREATE TABLE IF NOT EXISTS psessions (
    id varchar(64) not null primary key,
    a_session jsonb
);
CREATE INDEX IF NOT EXISTS i_p__session_kind    ON psessions ((a_session ->> '_session_kind'));
CREATE INDEX IF NOT EXISTS i_p__httpSessionType ON psessions ((a_session ->> '_httpSessionType'));
CREATE INDEX IF NOT EXISTS i_p__session_uid     ON psessions ((a_session ->> '_session_uid'));
CREATE INDEX IF NOT EXISTS i_p_ipAddr           ON psessions ((a_session ->> 'ipAddr'));
CREATE INDEX IF NOT EXISTS i_p__whatToTrace     ON psessions ((a_session ->> '_whatToTrace'));


CREATE UNLOGGED TABLE IF NOT EXISTS samlsessions (
    id varchar(64) not null primary key,
    a_session jsonb
);
CREATE INDEX IF NOT EXISTS i_a__session_kind ON samlsessions ((a_session ->> '_session_kind'));
CREATE INDEX IF NOT EXISTS i_a__utime        ON samlsessions ((cast(a_session ->> '_utime' as bigint)));
CREATE INDEX IF NOT EXISTS i_a_ProxyID       ON samlsessions ((a_session ->> 'ProxyID'));
CREATE INDEX IF NOT EXISTS i_a__nameID       ON samlsessions ((a_session ->> '_nameID'));
CREATE INDEX IF NOT EXISTS i_a__assert_id    ON samlsessions ((a_session ->> '_assert_id'));
CREATE INDEX IF NOT EXISTS i_a__art_id       ON samlsessions ((a_session ->> '_art_id'));
CREATE INDEX IF NOT EXISTS i_a__saml_id      ON samlsessions ((a_session ->> '_saml_id'));

CREATE UNLOGGED TABLE IF NOT EXISTS oidcsessions (
    id varchar(64) not null primary key,
    a_session jsonb
);
CREATE INDEX IF NOT EXISTS i_o__session_kind ON oidcsessions ((a_session ->> '_session_kind'));
CREATE INDEX IF NOT EXISTS i_o__utime        ON oidcsessions ((cast(a_session ->> '_utime' as bigint )));

CREATE UNLOGGED TABLE IF NOT EXISTS cassessions (
    id varchar(64) not null primary key,
    a_session jsonb
);
CREATE INDEX IF NOT EXISTS i_c__session_kind ON cassessions ((a_session ->> '_session_kind'));
CREATE INDEX IF NOT EXISTS i_c__utime        ON cassessions ((cast(a_session ->> '_utime' as bigint)));
CREATE INDEX IF NOT EXISTS i_c__cas_id       ON cassessions ((a_session ->> '_cas_id'));
CREATE INDEX IF NOT EXISTS i_c_pgtIou        ON cassessions ((a_session ->> 'pgtIou'));