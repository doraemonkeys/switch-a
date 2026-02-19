
## Rules

- **Explain "Why", not "What"**: Use comments to explain design rationale, business logic constraints, or non-obvious trade-offs. Code structure and naming should inherently describe the "what."
- **Principle of Least Surprise**: Design logic to be intuitive. Code implementation must behave as a developer expects, and functional design must align with the user's intuition.
- **No Backward Compatibility**: Pre-v1.0 with no external consumers to protect. Prioritize first-principles domain modeling and logical orthogonality; favor refactoring core structures to capture native semantics over adding additive flags or 'patch' parameters. 
- **Hard Requirement**: Project CI enforces minimum test coverage — **90% for Go**, **40% for React**.
- **Avoid Hardcoding**: Extract unexplained numeric and string values into named constants.

## Go Specific
- **Design for Testability (DfT)**: Favor Dependency Injection and decoupled components. Define interfaces via Traits to allow easy mocking, and prefer small, pure functions that can be unit-tested in isolation.
- **Accept Interfaces, Return Structs**: Define interfaces where they are used (consumer side), not where they are implemented.
- Don't name your package util, common, or misc. Packages should differ by what they provide, not what they contain.
- Never store context inside a struct.

## React Specific
- **Declarative UI & One-way Flow:** Treat UI as $UI = f(state)$; props flow down, events bubble up. Use **derived state** during render instead of redundant syncing effects.
- **Strictly Modern Hooks:** Use `useEffect` *only* for external synchronization (APIs/subs). Handle all user interactions in event handlers. **Trust React Compiler** (no manual `useMemo`/`useCallback`).
- **Composition Strategy:** Prefer colocation and Composition (`children`) over deep prop drilling. Use Context only for truly global state. Extract logic into Custom Hooks.
- **Standards & Quality:** Use semantic HTML and never use array indexes as keys. Prioritize accessibility-first testing (e.g., `getByRole`) for robust code.

## Git

- **NEVER add `Authored-By` or `Co-Authored-By` trailers to commit messages.** No attribution lines of any kind (including `via [HAPI]` or similar).

## Other

- Sub-agent policy: DO NOT use `explorer` type; use `default` or `worker` only

### Response Format

After finishing work, output:

```
---
🤖 Model: [model name]
---
```