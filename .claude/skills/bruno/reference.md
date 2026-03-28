# Bruno Detailed Reference

## Bru Lang Specification

### Block Types

**Dictionary Block** — key-value pairs enclosed in `{}`
```bru
headers {
  content-type: application/json
  Authorization: Bearer {{token}}
  ~x-debug: true
}
```
- Keys and values separated by `: `
- `~` prefix disables the entry
- Used for: HTTP method, params, headers, auth, vars

**Text Block** — freeform text enclosed in `{}`
```bru
body:json {
  {
    "username": "john",
    "email": "john@example.com"
  }
}

tests {
  test("status is 200", function() {
    expect(res.status).to.equal(200);
  });
}
```
- Used for: body types, scripts, tests, docs

**Array Block** — comma-separated list in `[]`
```bru
vars:secret [
  apiKey,
  clientSecret,
  ~disabledVar
]
```
- Used for: secret variables

### All Bru Tags

| Tag | Type | Purpose |
|-----|------|---------|
| `meta` | dict | Request metadata (name, type, seq, tags) |
| `get/post/put/patch/delete/options/head/trace/connect` | dict | HTTP method + url + body type + auth |
| `params:query` | dict | Query string parameters |
| `params:path` | dict | Path parameters |
| `headers` | dict | Request headers |
| `auth:bearer` | dict | Bearer token auth |
| `auth:basic` | dict | Basic auth (username/password) |
| `auth:apikey` | dict | API key auth (key/value/placement) |
| `auth:digest` | dict | Digest auth |
| `auth:oauth2` | dict | OAuth 2.0 auth |
| `auth:awsv4` | dict | AWS Signature V4 auth |
| `body:json` | text | JSON request body |
| `body:text` | text | Plain text body |
| `body:xml` | text | XML body |
| `body:form-urlencoded` | dict | URL-encoded form body |
| `body:multipart-form` | dict | Multipart form body |
| `body:graphql` | text | GraphQL query |
| `body:graphql:vars` | text | GraphQL variables |
| `vars` | dict | Collection/folder variables |
| `vars:pre-request` | dict | Pre-request variables (set before script) |
| `vars:post-response` | dict | Post-response variables (set after script) |
| `vars:secret` | array | Secret variable names |
| `script:pre-request` | text | JavaScript pre-request script |
| `script:post-response` | text | JavaScript post-response script |
| `tests` | text | Chai.js test assertions |
| `docs` | text | Markdown documentation |
| `assert` | dict | Quick assertions (alternative to tests block) |

### Assertions Block (Alternative to Tests)

```bru
assert {
  res.status: eq 200
  res.body.name: eq John
  res.body.data: isArray
  res.body.count: gt 0
  res.responseTime: lt 2000
}
```

Operators: `eq`, `neq`, `gt`, `gte`, `lt`, `lte`, `in`, `notIn`, `contains`, `notContains`, `matches`, `notMatches`, `startsWith`, `endsWith`, `between`, `length`, `minLength`, `maxLength`, `isString`, `isNumber`, `isBoolean`, `isArray`, `isNull`, `isUndefined`, `isDefined`, `isTruthy`, `isFalsy`, `isJson`, `isEmpty`, `isNotEmpty`

## Variable Precedence (highest to lowest)

1. Runtime variables (`bru.setVar()`)
2. Pre-request variables (`vars:pre-request`)
3. Request variables
4. Folder variables
5. Collection variables
6. Environment variables
7. Global environment variables
8. Process environment variables (`process.env`)

## Request Object (req) — Full API

### URL & Routing
| Method | Returns | Description |
|--------|---------|-------------|
| `req.getUrl()` | string | Current request URL |
| `req.setUrl(url)` | void | Set request URL |
| `req.getHost()` | string | Hostname from URL |
| `req.getPath()` | string | Path from URL |
| `req.getQueryString()` | string | Raw query string |
| `req.getPathParams()` | object | Path parameters as object |

### HTTP Method
| Method | Returns | Description |
|--------|---------|-------------|
| `req.getMethod()` | string | Current HTTP method |
| `req.setMethod(method)` | void | Set HTTP method |

### Headers
| Method | Returns | Description |
|--------|---------|-------------|
| `req.getHeader(name)` | string | Header value by name |
| `req.getHeaders()` | object | All headers |
| `req.setHeader(name, value)` | void | Set single header |
| `req.setHeaders(headers)` | void | Set multiple headers (object) |
| `req.deleteHeader(name)` | void | Remove single header |
| `req.deleteHeaders([names])` | void | Remove multiple headers |

### Body
| Method | Returns | Description |
|--------|---------|-------------|
| `req.getBody(options?)` | any | Body (use `{raw: true}` for unparsed) |
| `req.setBody(body)` | void | Set request body |

### Config
| Method | Returns | Description |
|--------|---------|-------------|
| `req.setTimeout(ms)` | void | Set request timeout |
| `req.getTimeout()` | number | Get timeout value |
| `req.setMaxRedirects(n)` | void | Max redirects to follow |

### Info
| Method | Returns | Description |
|--------|---------|-------------|
| `req.getName()` | string | Request name |
| `req.getTags()` | string[] | Request tags |
| `req.getAuthMode()` | string | Auth mode |
| `req.getExecutionMode()` | "runner" \| "standalone" | Execution context |
| `req.getExecutionPlatform()` | "app" \| "cli" | Platform |
| `req.onFail(callback)` | void | Error handler (dev sandbox only) |

## Response Object (res) — Full API

### Properties
| Property | Type | Description |
|----------|------|-------------|
| `res.status` | number | HTTP status code |
| `res.statusText` | string | HTTP status text |
| `res.headers` | object | Response headers |
| `res.body` | any | Parsed response body |
| `res.responseTime` | number | Response time in ms |
| `res.url` | string | Final URL (after redirects) |

### Methods
| Method | Returns | Description |
|--------|---------|-------------|
| `res.getStatus()` | number | Status code |
| `res.getStatusText()` | string | Status text |
| `res.getHeader(name)` | string | Header by name |
| `res.getHeaders()` | object | All headers |
| `res.getBody()` | any | Response body |
| `res.setBody(body)` | void | Modify response body |
| `res.getResponseTime()` | number | Time in ms |
| `res.getUrl()` | string | Final URL |
| `res.getSize()` | {body, headers, total} | Size in bytes |

## Bruno Runtime (bru) — Full API

### Variables
| Method | Description |
|--------|-------------|
| `bru.getVar(key)` | Get runtime variable |
| `bru.setVar(key, value)` | Set runtime variable |
| `bru.hasVar(key)` | Check if runtime variable exists |
| `bru.deleteVar(key)` | Delete runtime variable |
| `bru.getEnvVar(key)` | Get environment variable |
| `bru.setEnvVar(key, value)` | Set environment variable |
| `bru.hasEnvVar(key)` | Check if env variable exists |
| `bru.deleteEnvVar(key)` | Delete environment variable |
| `bru.getEnvName()` | Get current environment name |
| `bru.getCollectionVar(key)` | Get collection variable |
| `bru.setCollectionVar(key, value)` | Set collection variable |
| `bru.getFolderVar(key)` | Get folder variable |
| `bru.setFolderVar(key, value)` | Set folder variable |
| `bru.getRequestVar(key)` | Get request variable |
| `bru.getProcessEnv(key)` | Get process.env variable |
| `bru.getGlobalEnvVar(key)` | Get global environment variable |
| `bru.setGlobalEnvVar(key, value)` | Set global environment variable |

### Flow Control
| Method | Description |
|--------|-------------|
| `bru.setNextRequest(name)` | Chain to named request |
| `bru.sleep(ms)` | Pause execution |
| `bru.interpolate(string)` | Resolve `{{variables}}` in string |
| `bru.runner.skipRequest()` | Skip current request in collection run |
| `bru.runner.stopExecution()` | Stop collection run |
| `bru.runner.setNextRequest(name)` | Alternative chain syntax |

### Cookies
| Method | Description |
|--------|-------------|
| `bru.cookies.jar()` | Get cookie jar |
| `jar.setCookie(url, name, value)` | Set cookie |
| `jar.getCookie(url, name)` | Get cookie (async) |
| `jar.getCookies(url)` | Get all cookies for URL (async) |

## Dynamic Variables

Use directly in `.bru` files or via `bru.interpolate()`:

| Variable | Description |
|----------|-------------|
| `{{$guid}}` | Random UUID |
| `{{$timestamp}}` | Unix timestamp (seconds) |
| `{{$isoTimestamp}}` | ISO 8601 timestamp |
| `{{$randomInt}}` | Random integer |
| `{{$randomEmail}}` | Random email |
| `{{$randomFirstName}}` | Random first name |
| `{{$randomLastName}}` | Random last name |
| `{{$randomPhoneNumber}}` | Random phone number |
| `{{$randomCity}}` | Random city name |
| `{{$randomCountry}}` | Random country name |

## Bruno CLI Reference

### Installation
```bash
npm install -g @usebruno/cli
```

### Commands

```bash
bru run [filename|foldername] [options]
```

### Options
| Option | Description |
|--------|-------------|
| `--env <name>` | Select environment |
| `--env-var <key=value>` | Override env variable (repeatable) |
| `-r, --recursive` | Run requests in subfolders |
| `--tag <tag>` | Filter by tag (repeatable, comma-separated) |
| `--bail` | Stop on first failure |
| `--insecure` | Skip SSL verification |
| `--cacert <path>` | Custom CA certificate |
| `--timeout <ms>` | Request timeout |
| `--reporter-html <path>` | Generate HTML report |
| `--reporter-junit <path>` | Generate JUnit XML report |
| `--reporter-json <path>` | Generate JSON report |
| `--output <path>` | Output directory for reports |
| `--sandbox <mode>` | Sandbox mode: `safe` (default) or `developer` |
| `--delay <ms>` | Delay between requests |
| `--csv-file-path <path>` | CSV data file for data-driven tests |

### GitHub Actions Example

```yaml
name: API Tests
on: [push, pull_request]

jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-node@v4
        with:
          node-version: '20'
      - run: npm install -g @usebruno/cli
      - run: bru run --env ci --reporter-html report.html --reporter-junit report.xml
        working-directory: ./bruno-collection
      - uses: actions/upload-artifact@v4
        if: always()
        with:
          name: test-reports
          path: |
            ./bruno-collection/report.html
            ./bruno-collection/report.xml
```

## Collection-Level and Folder-Level Scripts

### collection.bru (at collection root)

```bru
script:pre-request {
  // Runs before EVERY request in the collection
  const token = bru.getEnvVar("authToken");
  if (token) {
    req.setHeader("Authorization", "Bearer " + token);
  }
}

script:post-response {
  // Runs after EVERY request in the collection
  if (res.status === 401) {
    console.log("Auth failed - token may be expired");
  }
}
```

### folder.bru (inside a folder)

```bru
headers {
  x-folder-header: value
}

script:pre-request {
  // Runs before every request in THIS folder
}
```

## Script Execution Order

1. Collection pre-request script
2. Folder pre-request script
3. Request pre-request variables (`vars:pre-request`)
4. Request pre-request script
5. **HTTP Request executes**
6. Request post-response variables (`vars:post-response`)
7. Request post-response script
8. Collection post-response script
9. Request tests
