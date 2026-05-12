# Adding gount to your website

Drop one of the snippets below into any page. Every hit records the visitor's country, page path, referrer, and a privacy-safe anonymous ID. No cookies, no personal data stored.

---

## Option 1 — Script (recommended)

Fires automatically on page load. Captures the current path from the browser and forwards any `?ref=` parameter as the referrer.

```html
<script>
  var ref = new URLSearchParams(window.location.search).get('ref');
  var url = 'https://tracker.example.com/t?p=' + encodeURIComponent(window.location.pathname);
  if (ref) url += '&ref=' + encodeURIComponent(ref);
  fetch(url, { keepalive: true });
</script>
```

Place it just before `</body>`. The `keepalive: true` flag helps the ping complete even if the visitor navigates away immediately.

---

## Option 2 — Tracking pixel (no JavaScript)

A 1×1 invisible image. Works without JavaScript and in HTML emails, but you set the path and referrer manually in the URL.

```html
<img src="https://tracker.example.com/t?p=homepage" width="1" height="1" style="display:none" alt="">
```

### Parameters

| Parameter | What it does | Example |
|---|---|---|
| `p` | The page or item name being tracked | `p=homepage`, `p=shop/item-42` |
| `ref` | Where the visitor came from | `ref=newsletter`, `ref=twitter` |

Both parameters are optional, but recommended.

**Full example — tracking a product page linked from an email campaign:**

```html
<img src="https://tracker.example.com/t?p=shop/item-42&ref=email-may" width="1" height="1" style="display:none" alt="">
```

This records:

```text
path     -> shop/item-42
referrer -> email-may
```

---

## Using both together

The script handles JavaScript-enabled browsers; the pixel catches everything else.

```html
<script>
  var ref = new URLSearchParams(window.location.search).get('ref');
  var url = 'https://tracker.example.com/t?p=' + encodeURIComponent(window.location.pathname);
  if (ref) url += '&ref=' + encodeURIComponent(ref);
  fetch(url, { keepalive: true });
</script>
<noscript>
  <img src="https://tracker.example.com/t?p=homepage" width="1" height="1" style="display:none" alt="">
</noscript>
```

---

## What gets recorded per visit

| Field | Example | Notes |
|---|---|---|
| `path` | `/shop/item-42` | From the `?p=` parameter |
| `referrer` | `google.com` | From `?ref=` or the browser `Referer` host |
| `country` | `CA` | ISO country code from GeoIP |
| `city` | `Toronto` | Only populated when `geo_type: city` |
| `unique_id` | `a3f9...` | Hashed — raw IP is never stored |
| `timestamp` | `1746959203` | Unix seconds (UTC) |

---

## Attributing traffic sources with `?ref=`

Add `?ref=<source>` to any link pointing at your site to tag where that traffic came from. The tracker picks it up automatically.

```text
https://yoursite.com/landing?ref=newsletter
https://yoursite.com/landing?ref=twitter
https://yoursite.com/landing?ref=partner-acme
```

If someone arrives without a `?ref=` and without a browser `Referer` header, the referrer is stored as `Direct`.

## Reverse proxy note

`X-Forwarded-For` and `X-Real-IP` are only used when the request comes from a configured `trusted_proxies` IP or CIDR. If `trusted_proxies` is empty, gount ignores proxy headers and uses the direct socket address.
