# settings

Reads tenant settings and feature flags by name, so a renamed key keeps
working under both its old and its new name, and logs who still reads the
old one.

Schema and decisions: dashboard-app ADR 0013 and
`docs/settings-standardization.md` (PLAT-136).

| Table | Holds | Naming |
|---|---|---|
| `feature_flags` | Cata-controlled flags (tenant may use a feature) | `FF_<FEATURE>`, `is_enabled` 0/1 |
| `settings` | Merchant toggles and every other setting | toggles: `IS_ENABLE_<FEATURE>`, `'true'`/`'false'` |
| `setting_aliases` | legacy key → new key, target table, source of truth | — |

## Use

```go
import "github.com/sambatechno/svclib/settings"

// Once per process. Store is your DB adapter (see below).
resolver := settings.New(store,
    settings.WithService("backoffice-v2-be"),           // default: $K_SERVICE
    settings.WithTenantFunc(func(ctx context.Context) string { ... }), // default: svclib.GetTenantID
)

on, err := resolver.IsEnabled(ctx, "FF_SPECIAL_TIER")
values, err := resolver.GetMany(ctx, []string{"CURRENCY", "IS_SHOW_SPECIAL_TIER"})
```

| You ask for | You get |
|---|---|
| A plain setting | `settings.value` |
| `FF_<X>` | `"true"` / `"false"` from `feature_flags`; the legacy setting when the flag row is missing |
| A legacy key | The value of its replacement, plus a deprecation warning |
| Either name of a settings alias | The row named by `source_of_truth` (the other row if it is missing) |

Pass the aliases your service already knows with
`settings.WithBuiltinAliases(...)`. They apply when `setting_aliases` can't be
read or has no row for a key (for example when the service deploys before the
migration has run); a database row always wins.

Only a failed `settings` read is an error. If `setting_aliases` or
`feature_flags` can't be read (for example, the tenant's migration hasn't run
yet), the resolver logs once and answers from `settings` alone.

Values: `settings.ParseBool` accepts `true`, `1`, `yes`, `on`, `enable`,
`enabled` (any case). `settings.FormatBool` writes `"true"` / `"false"`.

## Store

Implement `settings.Store` on top of your query layer, or use `SQLStore` for
plain `database/sql`:

```go
store := settings.SQLStore{
    DB: db,
    Table: func(ctx context.Context, table string) string {
        id, _ := svclib.GetTenantID(ctx)
        return fmt.Sprintf("`tenant%s`.%s", id, table)
    },
}
```

Keep your own value cache, if any, in front of the resolver's result rather
than the Store, so deprecation warnings still fire on cached reads.

## Finding services that still read legacy keys

Each legacy read logs a `WARNING` (stack trace included), at most once per
tenant and key every 10 minutes (`WithWarnInterval`):

```
severity=WARNING
jsonPayload.message="svclib/settings: deprecated setting key"
```

`jsonPayload.data` carries `service`, `tenant_id`, `legacy_key`, `new_key` and
`target`. Group by `service` and `legacy_key` to get the migration list.
