---
name: bruno
description: Generate, edit, and manage Bruno API client collections (.bru files), environments, tests, and scripts. Use when working with Bruno API collections, writing .bru files, creating API tests, or setting up Bruno CLI pipelines.
argument-hint: [action] [details]
allowed-tools: Read, Write, Edit, Glob, Grep, Bash, Agent
---

# Bruno API Client Skill

You are an expert at working with **Bruno**, the Git-first, offline-only API client. You generate and manage `.bru` files, environments, collection configurations, scripts, and tests.

For detailed reference, see [reference.md](reference.md).

## Key Principles

1. **Always use `.bru` format** — never JSON for request definitions
2. **Store secrets** in `vars:secret` blocks, never hardcoded
3. **Use environment variables** (`{{varName}}`) for values that change across environments
4. **Organize with folders** — group related requests logically
5. **Write tests** using Chai.js `expect` assertions
6. **Git-friendly** — all files are plain text, designed for version control

## Collection Structure

```
bruno-collection/
├── bruno.json              # Collection config (required)
├── collection.bru          # Collection-level settings (optional)
├── environments/
│   ├── dev.bru
│   ├── staging.bru
│   └── prod.bru
├── auth/
│   ├── login.bru
│   └── refresh-token.bru
├── users/
│   ├── get-users.bru
│   ├── get-user-by-id.bru
│   ├── create-user.bru
│   └── update-user.bru
└── ...
```

## bruno.json (Collection Config)

```json
{
  "version": "1",
  "name": "Collection Name",
  "type": "collection",
  "proxy": {
    "enabled": false
  },
  "scripts": {
    "moduleWhitelist": ["crypto", "buffer"]
  }
}
```

## .bru File Format

A `.bru` file uses the Bru markup language with three block types:
- **Dictionary blocks** — key-value pairs: `headers { key: value }`
- **Text blocks** — freeform content: `body:json { ... }`
- **Array blocks** — list of strings: `vars:secret [ key1, key2 ]`

Prefix any key with `~` to disable it (commented out but preserved).

### Complete Request Template

```bru
meta {
  name: Request Name
  type: http
  seq: 1
  tags: [
    smoke
    sanity
  ]
}

post {
  url: {{baseUrl}}/endpoint
  body: json
  auth: bearer
}

params:query {
  page: 1
  limit: 10
  ~debug: true
}

params:path {
  userId: {{userId}}
}

headers {
  accept: application/json
  x-api-key: {{apiKey}}
  ~x-debug: true
}

auth:bearer {
  token: {{authToken}}
}

body:json {
  {
    "field": "value",
    "nested": {
      "key": "{{dynamicVar}}"
    }
  }
}

script:pre-request {
  const timestamp = Date.now();
  bru.setVar("timestamp", timestamp);
}

script:post-response {
  if (res.status === 200) {
    bru.setVar("extractedId", res.body.id);
  }
}

tests {
  test("should return 200", function() {
    expect(res.status).to.equal(200);
  });

  test("should have correct structure", function() {
    expect(res.body).to.have.property("data");
    expect(res.body.data).to.be.an("array");
  });

  test("response time < 2s", function() {
    expect(res.responseTime).to.be.below(2000);
  });
}

docs {
  ## Description
  Brief description of this endpoint.

  ## Parameters
  - `field` (string, required): Description
}
```

### HTTP Methods

Use the lowercase method name as the block tag:

```bru
get { url: {{baseUrl}}/users }
post { url: {{baseUrl}}/users, body: json }
put { url: {{baseUrl}}/users/{{id}}, body: json }
patch { url: {{baseUrl}}/users/{{id}}, body: json }
delete { url: {{baseUrl}}/users/{{id}} }
options { url: {{baseUrl}}/users }
head { url: {{baseUrl}}/users }
```

### Body Types

```bru
# JSON
body:json {
  { "key": "value" }
}

# Form URL-encoded
body:form-urlencoded {
  username: john
  password: {{password}}
}

# Multipart form
body:multipart-form {
  file: @file(/path/to/file.pdf)
  field: value
}

# XML
body:xml {
  <root><item>value</item></root>
}

# Text
body:text {
  Plain text body content
}

# GraphQL
body:graphql {
  query { users { id name } }
}

body:graphql:vars {
  { "limit": 10 }
}
```

### Auth Types

```bru
# Bearer token
auth:bearer {
  token: {{authToken}}
}

# Basic auth
auth:basic {
  username: {{username}}
  password: {{password}}
}

# API Key
auth:apikey {
  key: x-api-key
  value: {{apiKey}}
  placement: header
}

# Inherit from collection/folder
auth { mode: inherit }

# No auth
auth { mode: none }
```

## Environment Files

```bru
vars {
  baseUrl: https://api.example.com
  apiVersion: v1
  timeout: 5000
}

vars:secret [
  apiKey,
  clientSecret,
  authToken
]
```

## JavaScript API Quick Reference

### Pre-Request Scripts (`script:pre-request`)

```javascript
// Request manipulation
req.getUrl() / req.setUrl(url)
req.getMethod() / req.setMethod(method)
req.getHeader(name) / req.setHeader(name, value)
req.getHeaders() / req.setHeaders(headers)
req.deleteHeader(name) / req.deleteHeaders([names])
req.getBody() / req.setBody(body)
req.setTimeout(ms) / req.getTimeout()
req.setMaxRedirects(count)
req.getName() / req.getTags()
req.getHost() / req.getPath() / req.getQueryString()
req.getPathParams()
req.getExecutionMode()  // "runner" | "standalone"
req.getExecutionPlatform()  // "app" | "cli"
req.onFail(callback)  // developer sandbox only

// Bruno runtime
bru.getVar(key) / bru.setVar(key, value)
bru.getEnvVar(key) / bru.setEnvVar(key, value)
bru.hasEnvVar(key) / bru.getEnvName()
bru.getProcessEnv(key)
bru.getCollectionVar(key) / bru.setCollectionVar(key, value)
bru.getFolderVar(key) / bru.setFolderVar(key, value)
bru.getRequestVar(key)
bru.sleep(ms)
bru.interpolate(string)  // resolve {{variables}} including dynamic ones
bru.runner.skipRequest()  // skip in collection runs
bru.runner.stopExecution()  // stop collection run
bru.setNextRequest(name)  // chain requests

// Dynamic variables (use in interpolation or directly in .bru files)
// {{$guid}}, {{$timestamp}}, {{$isoTimestamp}}, {{$randomInt}}
// {{$randomEmail}}, {{$randomFirstName}}, {{$randomLastName}}
// {{$randomPhoneNumber}}, {{$randomCity}}, {{$randomCountry}}
```

### Post-Response Scripts (`script:post-response`)

```javascript
// Response object
res.status / res.getStatus()
res.statusText / res.getStatusText()
res.headers / res.getHeader(name) / res.getHeaders()
res.body / res.getBody() / res.setBody(body)
res.responseTime / res.getResponseTime()
res.url / res.getUrl()
res.getSize()  // { body, headers, total } in bytes
```

### Test Scripts (`tests`)

Uses Chai.js `expect` syntax:

```javascript
test("description", function() {
  expect(res.status).to.equal(200);
  expect(res.body).to.have.property("data");
  expect(res.body.data).to.be.an("array").that.is.not.empty;
  expect(res.body.name).to.be.a("string");
  expect(res.body.email).to.match(/^[\w-.]+@([\w-]+\.)+[\w-]{2,4}$/);
  expect(res.responseTime).to.be.below(2000);
  expect(res.getHeader("content-type")).to.include("application/json");
  expect(res.getSize().body).to.be.lessThan(1024);
});
```

### Cookie Management

```javascript
const jar = bru.cookies.jar();
jar.setCookie("https://example.com", "session", "abc123");
const cookie = await jar.getCookie("https://example.com", "session");
```

## Bruno CLI

Install: `npm install -g @usebruno/cli`

```bash
# Run entire collection
bru run --env dev

# Run specific request
bru run request.bru --env dev

# Run folder
bru run folder/ --env dev

# Run with tags filter
bru run --tag smoke --env dev

# Generate reports
bru run --env dev --reporter-html results.html
bru run --env dev --reporter-junit results.xml

# With .env secrets
bru run --env dev --env-var API_KEY=secret

# Bail on first failure
bru run --env dev --bail

# With custom CA cert
bru run --env dev --cacert /path/to/cert.pem

# Insecure (skip SSL verification)
bru run --env dev --insecure
```

## When Generating Collections from Source Code

1. **Read the source routes/controllers** to discover endpoints
2. **Create `bruno.json`** at collection root
3. **Create environment files** for each stage (dev, staging, prod)
4. **Create `.bru` files** for each endpoint with:
   - Correct HTTP method and URL with path params
   - Request headers and auth
   - Example request body
   - Pre-request scripts if needed (e.g., setting dynamic values)
   - Post-response scripts for extracting tokens/IDs
   - Tests for status code, response structure, and edge cases
   - Documentation describing the endpoint
5. **Organize in folders** matching the API resource structure
6. **Use `{{variables}}`** for base URL, auth tokens, IDs, etc.

## Important Notes

- File names should be kebab-case: `get-user-by-id.bru`
- The `seq` field in meta controls sort order in the Bruno UI
- `~` prefix disables a header/param without removing it
- `body` field in HTTP method block must match body type: `json`, `xml`, `text`, `formUrlEncoded`, `multipartForm`, `graphql`, `none`
- `auth` field: `bearer`, `basic`, `apikey`, `inherit`, `none`
- Collection-level scripts in `collection.bru` run before/after every request
- Folder-level scripts in `folder.bru` run for requests in that folder
