# Main server debug: 401 "Server not found or unauthorized"

Use these on the **main server** (dcp1) to investigate why a client gets 401 on `/pending-action`, `/torrent-status`, heartbeat, and inventory sync.

**From your client log:**
- Client reports **Server ID:** `d4f6b1e9-4707-4d48-a1f0-72a6693b58e0`
- Client reports **MAC:** `D4:F5:EF:90:D0:C0`
- API requests are sent with URL path **server id:** `796285a3-137d-4db5-930a-2c0ff252bfa4` (see log: `.../servers/796285a3-137d-4db5-930a-2c0ff252bfa4/pending-action`)
- Package ID you asked about: `699e52fc-1bb5-433d-9cd3-8c80563f6130`

The main server auth middleware looks up the server by **ID from the URL** (or `X-Server-ID` header), then checks `is_authorized`. If no row is found → 401 "Server not found or unauthorized". If row found but `is_authorized = false` → 403 "Server not authorized".

---

## 1. Connect to main server database

Use the main server’s DB (e.g. from dcp1, with your real password):

```bash
# On main server (dcp1)
psql -h localhost -U omni -d OmniCloud
# or
PGPASSWORD='your_main_server_db_password' psql -h localhost -U omni -d OmniCloud
```

---

## 2. List all servers (overview)

```sql
SELECT id, name, location, mac_address, is_authorized, last_seen, software_version
FROM servers
ORDER BY name;
```

Check:
- Is there a row with `id = '796285a3-137d-4db5-930a-2c0ff252bfa4'`?
- Is there a row with `id = 'd4f6b1e9-4707-4d48-a1f0-72a6693b58e0'`?
- Which row has `mac_address = 'D4:F5:EF:90:D0:C0'`?

---

## 3. Look up by the ID the client sends in the URL (796285a3...)

This is the ID used for pending-action and other protected routes.

```sql
SELECT id, name, location, api_url, mac_address, is_authorized, registration_key_hash IS NOT NULL AND registration_key_hash != '' AS has_key_hash, last_seen, software_version, created_at
FROM servers
WHERE id = '796285a3-137d-4db5-930a-2c0ff252bfa4';
```

- **0 rows** → main server has no record of this ID → 401 "Server not found or unauthorized". The client may be using a different ID than the one it got when registering (e.g. local DB ID vs main-server-assigned ID).
- **1 row, is_authorized = false** → you’d get 403 "Server not authorized", not 401. So if you see 401, this query usually returns 0 rows.

---

## 4. Look up by the ID the client prints in the log (d4f6b1e9...)

```sql
SELECT id, name, location, api_url, mac_address, is_authorized, last_seen, software_version, created_at
FROM servers
WHERE id = 'd4f6b1e9-4707-4d48-a1f0-72a6693b58e0';
```

If this returns a row but (3) does not, the client is calling the API with `796285a3...` while the main server only knows `d4f6b1e9...` (e.g. client_sync updated to d4f6b1e9 after register, but the update agent still uses 796285a3).

---

## 5. Look up by MAC (source of truth for “this machine”)

```sql
SELECT id, name, location, mac_address, is_authorized, last_seen, software_version
FROM servers
WHERE mac_address = 'D4:F5:EF:90:D0:C0';
```

- Which `id` does this row have? That’s the ID the main server associates with this client.
- Is `is_authorized` true? If false, once the client uses this ID you’ll get 403 until an admin authorizes.

---

## 6. Check for duplicate or stale rows (same MAC, multiple IDs)

```sql
SELECT id, name, mac_address, is_authorized, last_seen, created_at
FROM servers
WHERE mac_address = 'D4:F5:EF:90:D0:C0'
   OR id IN ('796285a3-137d-4db5-930a-2c0ff252bfa4', 'd4f6b1e9-4707-4d48-a1f0-72a6693b58e0')
ORDER BY created_at;
```

Interpretation:
- Two rows with same MAC → duplicate registrations; one may be old.
- One row with MAC and id = d4f6b1e9, and no row for 796285a3 → client is calling with 796285a3 but main server only has d4f6b1e9 → 401.

---

## 7. Authorization status for this client

```sql
SELECT id, name, is_authorized, last_seen
FROM servers
WHERE mac_address = 'D4:F5:EF:90:D0:C0';
```

If `is_authorized = false`, the UI or API needs to set this server to authorized (e.g. “Authorize” in admin) so it can sync and call protected endpoints.

---

## 8. Registration key consistency (optional)

Main server stores a hash of the registration key. Client must send the same key when registering.

```sql
SELECT id, name, LEFT(registration_key_hash, 20) AS key_hash_prefix, LENGTH(registration_key_hash) AS key_hash_len
FROM servers
WHERE mac_address = 'D4:F5:EF:90:D0:C0';
```

If the client’s `auth.config` has a different `registration_key` than the main server’s configured key, re-registration can fail or create a new row; that doesn’t directly cause 401 on pending-action, but it can lead to ID mismatch.

---

## 9. Package 699e52fc-1bb5-433d-9cd3-8c80563f6130 (for context)

Package exists on the **client**; the main server may or may not have it depending on inventory sync and authorization.

On **main server**:

```sql
-- Does the package exist?
SELECT id, package_name, assetmap_uuid, total_size_bytes
FROM dcp_packages
WHERE id = '699e52fc-1bb5-433d-9cd3-8c80563f6130';

-- Inventory for this package (which servers report having it?)
SELECT server_id, local_path, status
FROM server_dcp_inventory i
JOIN dcp_packages p ON p.id = i.package_id
WHERE p.id = '699e52fc-1bb5-433d-9cd3-8c80563f6130';
```

If the client is 401/403, inventory for this package may never have been synced.

---

## 10. One-shot summary query (run this and keep the result)

```sql
SELECT
  id,
  name,
  mac_address,
  is_authorized,
  last_seen,
  software_version,
  CASE WHEN id = '796285a3-137d-4db5-930a-2c0ff252bfa4' THEN '<< ID IN API URL' ELSE '' END AS note_796,
  CASE WHEN id = 'd4f6b1e9-4707-4d48-a1f0-72a6693b58e0' THEN '<< ID IN CLIENT LOG' ELSE '' END AS note_d4f
FROM servers
WHERE mac_address = 'D4:F5:EF:90:D0:C0'
   OR id IN ('796285a3-137d-4db5-930a-2c0ff252bfa4', 'd4f6b1e9-4707-4d48-a1f0-72a6693b58e0');
```

Interpretation:
- No row with `796285a3...` → 401 on pending-action/torrent-status is expected; the client is sending an ID the main server doesn’t have.
- Row with `d4f6b1e9...` and MAC `D4:F5:EF:90:D0:C0` → that’s the “real” client row; it should be authorized and the client should use this ID for all API calls (code change would be needed so the update agent uses the same ID as client_sync after registration).

---

## 11. How to authorize the server (once you know the correct row)

If the correct server row has `is_authorized = false`, set it to true (replace with the ID that has the MAC above, usually d4f6b1e9):

```sql
-- Use the id that matches MAC D4:F5:EF:90:D0:C0 (check query 5/10 first)
UPDATE servers
SET is_authorized = true, updated_at = CURRENT_TIMESTAMP
WHERE mac_address = 'D4:F5:EF:90:D0:C0';
-- Or by ID:
-- WHERE id = 'd4f6b1e9-4707-4d48-a1f0-72a6693b58e0';
```

After this, requests that use **that** server ID (and correct headers) should get 200, not 403. Requests that still use `796285a3...` will continue to get 401 until the client is fixed to use the same ID everywhere.

---

## Summary checklist

| Check | What to run | What you care about |
|-------|-------------|----------------------|
| All servers | Query 2 | See both IDs and MAC |
| ID in URL (796285a3) | Query 3 | Must exist for 401 to go away for that ID |
| ID in log (d4f6b1e9) | Query 4 | Likely the row created by registration |
| MAC | Query 5 | Which id the main server ties to this machine |
| Duplicates | Query 6 | Same MAC, two IDs |
| Authorized? | Query 7 | Must be true for 403 to go away |
| One-shot | Query 10 | Single result set to interpret |
| Authorize | Query 11 | After you know the right id, set is_authorized = true |

No code changes were made; this file is for debugging on the main server only.
