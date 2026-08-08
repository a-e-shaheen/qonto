# Architecture

Component/dependency view. Mermaid renders natively on GitHub — no extra tooling
needed to view this.

```mermaid
graph TD
    Main["cmd/server/main.go"]

    subgraph "internal/transfer"
        Handler["server — HTTP handlers,<br/>validation, RED + business metrics"]
        Service["service — idempotency guard,<br/>funds check, Atomic() orchestration"]
        StoreLayer["store — squirrel-built SQL<br/>+ one pgx CopyFrom"]
    end

    subgraph pkg
        Txn["txn — Atomic() unit-of-work,<br/>transaction-duration metric"]
        DB["database — pool, migrate runner,<br/>seed bootstrap, pool-stat metric"]
        Obs["observability — OTel tracer<br/>+ meter provider bootstrap"]
        Srv["server — http.Server wrapper,<br/>graceful shutdown, logging + RED middleware"]
    end

    Postgres[("Postgres 16 + pg_partman")]
    LGTM[["lgtm: Grafana + Tempo + Mimir<br/>+ embedded OTel Collector"]]

    Main --> Handler
    Main --> DB
    Main --> Obs
    Main --> Srv
    Handler --> Service
    Handler --> Srv
    Service --> StoreLayer
    Service --> Txn
    StoreLayer --> Txn
    StoreLayer --> Postgres
    Txn --> Postgres
    DB --> Postgres
    Obs -. OTLP traces + metrics .-> LGTM
```

Handler → service → store: a standard layered split — handler owns transport
(decode, validate, status codes), service owns business rules (idempotency, funds
check), store owns SQL. Each layer only calls the one below it.

See also:
- [Bulk transfer request flow](./sequence-submit-bulk-transfer.md) — the actual
  concurrency/idempotency logic, step by step.
- [Schema](./schema.md) — tables, keys, and the partitioning boundary.
