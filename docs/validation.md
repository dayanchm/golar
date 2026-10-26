# Validation

The `validation` package checks structs against rules written in `validate`
tags. Rules are parsed once per struct type and cached, so a request only pays
for the checks themselves. It has no third-party dependencies.

```go
type RegisterRequest struct {
    Name     string `json:"name" validate:"required,max=100"`
    Email    string `json:"email" validate:"required,email"`
    Password string `json:"password" validate:"required,min=8,confirmed"`
    PasswordConfirmation string `json:"password_confirmation"`
}

app.Post("/register", func(c *golar.Context) error {
    var req RegisterRequest
    if err := c.Validate(&req); err != nil {
        return err
    }
    // req is valid
})
```

`c.Validate` binds the body with `c.Bind` (JSON, URL-encoded or multipart, by
`Content-Type`) and validates it. With the default error handler, a body that
cannot be decoded returns `400 Invalid request body`, an unsupported body type
`415` and invalid input `422` with the messages keyed by field name:

```json
{"errors": {"email": ["The email field must be a valid email address."]}}
```

Outside handlers, call `validation.Validate(v)` or
`validation.ValidateContext(ctx, v)`. They return `nil`, a
`*validation.Error` whose `Errors` field holds the messages, or another error
for an invalid tag or a failing database query.

## Rules

| Rule | Passes when |
| --- | --- |
| `required` | the value is present: not nil, not a blank string, not an empty slice or map, and not a zero number or `false` unless behind a pointer |
| `email` | the string is a bare address such as `ada@example.com` |
| `url` | the string is an absolute URL with a scheme and host |
| `uuid` | the string is a UUID in 8-4-4-4-12 form |
| `numeric` | the value is a number or a string that parses as one |
| `min=n`, `max=n` | the size is at least or at most `n` |
| `len=n` | the size is exactly `n` |
| `between=a b` | the size is between `a` and `b`, inclusive |
| `in=a b c` | the value is one of the space-separated values |
| `regex=pattern` | the string matches `pattern`; must be the last rule, so the pattern may contain commas |
| `confirmed` | the field equals `<json name>_confirmation` or `<Go name>Confirmation`; `confirmed=other` names the field |
| `unique=table.column` | no row has this value; `unique=table` uses the field name as the column |
| `exists=table.column` | at least one row has this value |

Size means characters for strings, the value for numbers, and the length for
slices, arrays and maps. A string with the `numeric` rule is sized by its value,
so `numeric,min=18` compares numbers.

As in Laravel, only `required` runs on missing or empty values: an optional
`email` field may be left empty. Use a pointer, such as `*int`, to require a
field that may legitimately be `0` or `false`.

Fields are named by their `json` tag, then their `form` tag, or their Go name
without either. Nested structs are validated with dotted keys such as
`address.city`, unless the nested struct itself failed a rule. Embedded
structs share the outer keys, as in `encoding/json`. Slices of structs are not validated element by element.
`validate:"-"` skips a field.

Invalid tags, such as an unknown rule or `min=abc`, return an error the first
time the type is validated.

## Database rules

`unique` and `exists` run a `COUNT(*)` through the query builder and need a
database on the validator:

```go
validation.Default.DB = db // *database.DB or *database.Tx
```

`unique` does not ignore the current record. To check uniqueness on update,
register a custom rule that adds the condition you need.

## Messages

Messages use `:attribute`, the field name with underscores replaced by spaces,
and `:param`, `:min`, `:max` and `:values` from the rule. Override them on the
validator, from most to least specific:

```go
v := validation.Default
v.Messages["email.unique"] = "This email is already registered."   // field.rule
v.Messages["min.string"] = "The :attribute needs :param characters." // rule.kind: string, numeric or array
v.Messages["required"] = ":attribute is required."                  // rule
v.Attributes["dob"] = "date of birth"                               // :attribute for a field key
```

## Custom rules

```go
validation.Register("slug", "The :attribute may only contain lowercase letters, digits and dashes.",
    func(ctx context.Context, f validation.Field) (bool, error) {
        return slugPattern.MatchString(f.Value.String()), nil
    })

type Post struct {
    Slug string `json:"slug" validate:"required,slug"`
}
```

`Field.Value` is the dereferenced `reflect.Value`, `Field.Param` the text after
`=`, and `Field.Sibling(name)` returns another field of the same struct.
Returning an error aborts validation instead of reporting a failed field.

Configure messages, attributes, rules and the database before serving
requests. `validation.New()` creates an independent validator.
