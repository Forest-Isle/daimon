## ADDED Requirements

### Requirement: Result type classification
Every tool result SHALL carry a `Type` field indicating the semantic type of the output. Supported types SHALL be: `text` (default), `image`, `file`, `reference`.

#### Scenario: Default text type
- **WHEN** a tool returns a result without setting the `Type` field
- **THEN** the result type SHALL default to `text`

#### Scenario: File type result
- **WHEN** a tool reads a file and returns its content
- **THEN** the tool MAY set `Type: file` and `FilePath` to the source file path, enabling downstream systems to handle file content specially

#### Scenario: Reference type result
- **WHEN** a large tool result is persisted to disk (via tool-result-persistence)
- **THEN** the inline result SHALL have `Type: reference` and `FilePath` pointing to the persisted file

### Requirement: Partial result indicator
Tool results SHALL carry an `IsPartial` boolean indicating whether the output was truncated or incomplete.

#### Scenario: Truncated result
- **WHEN** a tool result is persisted to disk and only a preview is kept inline
- **THEN** `IsPartial` SHALL be `true` in the inline result

#### Scenario: Complete result
- **WHEN** a tool result fits entirely inline
- **THEN** `IsPartial` SHALL be `false`

### Requirement: Extensible metadata map
Tool results SHALL carry an optional `Metadata` map (`map[string]any`) for tool-specific key-value pairs that don't warrant dedicated struct fields.

#### Scenario: HTTP tool metadata
- **WHEN** the HTTP tool makes a request and receives a response
- **THEN** it MAY set metadata keys like `status_code`, `content_type`, `response_time_ms`

#### Scenario: No metadata
- **WHEN** a tool does not set any metadata
- **THEN** the `Metadata` field SHALL be `nil` and SHALL NOT affect existing behavior

### Requirement: Backward compatibility
Existing code that reads only `Result.Output` and `Result.Error` SHALL continue to work without modification. New fields SHALL have zero-value defaults that preserve current behavior.

#### Scenario: Legacy tool implementation
- **WHEN** a tool returns `Result{Output: "hello", Error: ""}` without setting any new fields
- **THEN** the result SHALL behave identically to current behavior: `Type` defaults to `text`, `IsPartial` defaults to `false`, `Metadata` defaults to `nil`
