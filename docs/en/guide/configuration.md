# Configuration

## File responsibilities

`<workspace>/config.yml` is the desired state maintained by the operator. `config.lock.yml` records resolved module versions, providers, and host policy. Do not edit runtime state below `.anas/` by hand.

The structured YAML contains:

- `modules` for module selection;
- `global` for shared settings such as domain, email, and timezone;
- `administration` for bootstrap and module-local administrator policy;
- `identity` for directory and IAM provider selection;
- `dynamic_dns` for the selected DDNS implementation and DNS vendor;
- `rollback` for local snapshot policy;
- `modules.<name>` for enablement, identity protocol, and module parameters;
- `secrets` for explicitly supplied sensitive values;
- `env` for raw environment variables that have no structured field.

## Change and preview

Edit YAML directly or use the CLI:

```bash
anas config explain nextcloud.domain_prefix
anas config set global.timezone Asia/Singapore -w /srv/anas
anas config plan -w /srv/anas
anas apply -w /srv/anas
```

Some changes affect persistent state inside a service and require an explicit migration. ANAS rejects an ordinary apply and reports the risk. Use `anas apply --allow-risky` only after preparing the required migration; the flag removes the gate but does not rotate credentials or migrate a database for you.

## Secrets

Do not commit real secrets. `config secret list` returns names only; only the explicit `config secret get` operation returns clear text. Generated secrets live in the protected workspace runtime and are handled by the ANAS backup flow.
