# Set up a local store

The existing [Compose file](../compose.yml) runs PostgreSQL, Redis and Kafka. The Go API and sibling Next.js frontend run on the host. Existing volumes and stores are retained.

## Prepared demo

The store `demo1007` was prepared on 2026-10-07 in the local `ecommerce` database, at migration 15. It contains three published products, storefront VND prices, initial stock quantities of 30/20/15, free shipping, sport appearance, an owner, and a registered customer with a saved address. VietQR uses synthetic beneficiary details; do not transfer real money. MoMo is not configured.

Open `http://demo1007.example.test:3000` after adding this line to the Windows hosts file at `C:\Windows\System32\drivers\etc\hosts` from an administrator editor:

```text
127.0.0.1 demo1007.example.test
```

Owner email: `owner@demo1007.test`. Customer email: `customer@demo1007.test`. Their generated local password and fixture identifiers are in `.cache/demo1007.json` in the backend folder. This file is ignored by Git. Sign in at `/account`; use `/admin` for store management.

## Start services again

From the backend folder:

```powershell
docker compose up -d --wait --wait-timeout 90
Get-Content .env | ForEach-Object {
    if ($_ -match '^([A-Z][A-Z0-9_]*)=(.*)$') {
        [Environment]::SetEnvironmentVariable($matches[1], $matches[2], 'Process')
    }
}
go run ./cmd/migrate up
go run ./cmd/api
```

Use the existing local `.env`. For a fresh checkout, copy `.env.example` to `.env` and configure local values first. Keep the local database on loopback. In another terminal, run `npm run dev` from `e-commerce-ui`. Start `go run ./cmd/worker` in the backend folder when testing expiry and asynchronous processing.

## Create another store

1. A platform administrator provisions the store through `POST /api/v1/onboarding/stores` on `example.test`, using the configured platform bearer token. There is no public store-creation page. Provisioning creates the store and initial owner without signing the owner in.
2. Add the new store's hostname to local DNS or the hosts file. The subdomain is immutable.
3. Sign in as that owner at `http://<subdomain>.example.test:3000/account`.
4. Create categories/products and variants under `/admin/products`. Add VND prices under `/admin/prices`, add stock under `/admin/inventory`, then publish products.
5. Configure `/admin/shipping`, `/admin/payments`, and `/admin/appearance`.
6. Test guest checkout in a private browser window and registered checkout with a separate customer account. Confirm fixture VietQR payments manually, then process fulfillment in `/admin/orders`.

For a complete synthetic fixture instead of entering each item manually, run from the backend folder:

```powershell
node dev/seed-demo-store.mjs anotherdemo
```

The helper targets only `127.0.0.1:8080`, uses the configured local platform token, creates a separate store, and records progress/credentials in `.cache/<subdomain>.json`. Do not remove its manifest and rerun against an existing store. A normal rerun resumes recorded setup without adding stock again; uncertain results from an interrupted write may require inspection. It verifies public catalog reads and credential denial on the existing `shop` store, which must exist for that final check.

For provisioning only, after loading the local environment:

```powershell
$storeBody = @{
    subdomain = 'newshop'
    store_name = 'New test store'
    email = 'owner@newshop.test'
    password = 'Local-test-password-2026!'
} | ConvertTo-Json
Invoke-RestMethod -Uri 'http://127.0.0.1:8080/api/v1/onboarding/stores' `
    -Method Post -ContentType 'application/json' `
    -Headers @{ Host = 'example.test'; Authorization = "Bearer $env:PLATFORM_ADMIN_TOKEN" } `
    -Body $storeBody
```

The request hostname selects the store on tenant routes; adding a `store_id` to a payload cannot change tenant scope. See the [frontend README](../../e-commerce-ui/README.md) for cookie and routing configuration.
