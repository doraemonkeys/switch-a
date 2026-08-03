
## Rules

- **Explain "Why", not "What"**: Use comments to explain design rationale, business logic constraints, or non-obvious trade-offs. Code structure and naming should inherently describe the "what."
- **Design for Testability (DfT)**: Favor Dependency Injection and decoupled components. Define interfaces to allow easy mocking, and prefer small, pure functions that can be unit-tested in isolation.
- **Observability by Design**: Emit structured trace logs at critical workflow milestones, major control-flow transitions, and failure-prone or counterintuitive branches. Include stable operation or session identifiers and decision context sufficient to reconstruct the runtime path and diagnose unexpected behavior quickly.
- **Principle of Least Surprise**: Design logic to be intuitive. Code implementation must behave as a developer expects, and functional design must align with the user's intuition.
- **No Backward Compatibility**: Pre-v1.0 with no external consumers to protect. Prioritize first-principles domain modeling and logical orthogonality; favor refactoring core structures to capture native semantics over adding additive flags or 'patch' parameters. Deleting code or rewriting a component from scratch is allowed and encouraged when it yields a cleaner design.
- **Avoid Hardcoding**: Extract unexplained numeric and string values into named constants.
- **Prefer Deep Modules**: Avoid coupling all functionality at one layer; use meaningful module boundaries to contain complexity. Simply put, each folder should not contain more than 20 code files (excluding test files), otherwise the module is too large.
- **Concise User-Facing Docs**: Keep externally maintained docs (README, docs/) concise and easy to follow; nobody reads verbose documentation.
- **Semantic Precision**: Avoid ambiguous or overloaded fields.
- Don't name your package util, common, or misc. Packages should differ by what they provide, not what they contain.
- **Hard Requirement**: Project CI enforces minimum test coverage — **90% for Go**, **40% for React**.

## Go Specific
- **Accept Interfaces, Return Structs**: Define interfaces where they are used (consumer side), not where they are implemented. The bigger the interface, the weaker the abstraction.
- Never store context inside a struct.

## React Specific
- **Declarative UI & One-way Flow:** Treat UI as $UI = f(state)$; props flow down, events bubble up. Use **derived state** during render instead of redundant syncing effects.
- **Strictly Modern Hooks:** Use `useEffect` *only* for external synchronization (APIs/subs). Handle all user interactions in event handlers. **Trust React Compiler** (no manual `useMemo`/`useCallback`).
- **Composition Strategy:** Prefer colocation and Composition (`children`) over deep prop drilling. Use Context only for truly global state. Extract logic into Custom Hooks.

## Project Overview

- [OVERVIEW](docs/PROJECT_OVERVIEW.md)
