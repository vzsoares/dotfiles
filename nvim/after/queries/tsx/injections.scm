; extends

; Alpine.js directives with template strings: x-data={`...`}, x-init={`...`}, etc.
(jsx_attribute
  (property_identifier) @_attr
  (#lua-match? @_attr "^x%-")
  (jsx_expression
    (template_string) @injection.content
    (#offset! @injection.content 0 1 0 -1)
    (#set! injection.include-children)
    (#set! injection.language "javascript")))

; Alpine.js directives with regular strings: x-data="..."
(jsx_attribute
  (property_identifier) @_attr
  (#lua-match? @_attr "^x%-")
  (string
    (string_fragment) @injection.content
    (#set! injection.language "javascript")))
