# Transform rules

Transform rules change a webhook **before it is delivered** to your application: strip sensitive fields, rename keys, add static values or timestamps, and edit headers. They are stored per endpoint (`endpoints.transform_rules`) and edited in the dashboard's pipeline editor, whose live preview runs exactly the same code as the worker.

Rules are **declarative data**, not scripts: they cannot run arbitrary code, are validated before they are saved and execute in microseconds.

## Example

Incoming Stripe event:

```json
{"id":"evt_1","type":"invoice.paid","user_id":42,"card":{"number":"4242"},"data":{"object":{"id":"in_9"}}}
```

Rules:

```json
{
  "version": 1,
  "body": [
    {"op": "remove", "path": "card"},
    {"op": "rename", "from": "user_id", "to": "customerId"},
    {"op": "copy",   "from": "data.object.id", "to": "invoiceId"},
    {"op": "set",    "path": "source", "value": "stripe"},
    {"op": "set_timestamp", "path": "forwardedAt"}
  ],
  "headers": [
    {"op": "remove", "name": "Stripe-Signature"},
    {"op": "set",    "name": "X-Source", "value": "hookshield"}
  ]
}
```

Delivered body:

```json
{"customerId":42,"data":{"object":{"id":"in_9"}},"forwardedAt":"2026-11-03T15:04:05Z","id":"evt_1","invoiceId":"in_9","source":"stripe","type":"invoice.paid"}
```

## Body operations

Rules run **in order**; each one sees the result of the previous ones.

| `op` | Fields | Effect |
|---|---|---|
| `remove` | `path` | Deletes the field. Removing an array element sets it to `null` (arrays keep their length). |
| `rename` | `from`, `to` | Moves a value to another path (creating intermediate objects). |
| `copy` | `from`, `to` | Deep-copies a value; the source is kept. |
| `set` | `path`, `value` | Writes any JSON value (string, number, boolean, object, array, `null`). |
| `set_timestamp` | `path`, `format` | Writes the processing time: `rfc3339` (default, UTC), `unix` (seconds) or `unix_ms`. |
| `keep` | `paths` | Allow-list: removes everything except the listed paths. Ideal to forward only what you need. |

### Paths

- Dots separate object keys: `data.object.id`.
- Numbers index arrays: `items.0.sku`.
- `set`, `rename` and `copy` create missing objects along the way. Arrays are never grown; out-of-range indexes are ignored.
- Keys that contain a literal dot cannot be addressed (planned for a future rules version).

## Header operations

Header names are case-insensitive.

| `op` | Fields | Effect |
|---|---|---|
| `remove` | `name` | Deletes the header. |
| `rename` | `from`, `to` | Renames the header, keeping its values. |
| `set` | `name`, `value` | Sets the header to a single value (replaces existing values). |

Secrets for outbound authentication (e.g. `Authorization: Bearer {{secrets.MY_API_KEY}}`) are configured separately as **outbound headers** and injected by the dispatcher at delivery time, so they are never stored with the payload.

## Behaviour you can rely on

| Situation | What happens |
|---|---|
| No body rules | The body is forwarded **byte for byte** (no re-encoding, key order and whitespace preserved). |
| Body rules present | The JSON is re-encoded: keys are sorted alphabetically and whitespace is compacted. Numbers keep their exact original text (no rounding of large integers or decimals). |
| Body is not a JSON object (form data, XML, array, plain text) | Body rules are skipped and the body is forwarded unchanged; header rules still apply. A warning is recorded. |
| A path does not exist | That rule does nothing; a warning is recorded. |
| No rules at all | Only the original request is stored; the dispatcher forwards it as received. |

Warnings never block delivery. They appear in the log debugger next to the request.

## Validation

Invalid rules are rejected when saved, with every problem listed. Each problem has a section (`body`, `headers` or `rules`), the index of the rule and a translatable code:

| Code | Meaning |
|---|---|
| `transform.invalid_json` | Not valid JSON, or unknown top-level fields |
| `transform.unsupported_version` | `version` is not `1` |
| `transform.too_many_rules` | More than 100 rules in total |
| `transform.unknown_op` | `op` is not one of the operations above |
| `transform.path_required` | Missing or malformed `path` (empty segment such as `a..b`, more than 32 levels) |
| `transform.from_to_required` | `rename`/`copy` without both `from` and `to` |
| `transform.target_inside_source` | `rename`/`copy` whose `to` is `from` or inside it (`a` → `a.b`): a field cannot be copied into itself |
| `transform.value_required` | `set` without a valid JSON `value` |
| `transform.invalid_format` | `set_timestamp` with an unknown `format` |
| `transform.paths_required` | `keep` with an empty `paths` list |
| `transform.header_name_required` | Missing or invalid header name |
| `transform.invalid_header_value` | Header value contains a line break (header injection) |

## Limits

- At most 100 rules, paths of at most 32 levels.
- **Size budget:** `copy` is the only rule that can grow a body. All copies of one request together may add at most the size of the input plus 64 KiB (estimated JSON size). Rules that go past it — e.g. copies of a field into another field, back and forth, which would double the body each time — are **not applied**: the preview answers `422 transform.output_too_large`, and a real webhook goes to the dead-letter stream (`transform_too_large`) instead of being delivered. It is never delivered untransformed: the rules may remove fields that must not reach the destination.
- Stored rules that no longer pass validation (saved before a rule existed) fail closed the same way (`transform_invalid`).

## Where the code lives

`backend/internal/transform` (parsing, validation, `Apply`), exhaustively unit-tested in `transform_test.go`. The processor (`backend/internal/processor`) applies the rules and stores both the original and the transformed request, each encrypted separately.
