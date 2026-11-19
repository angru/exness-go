# Changelog

## Unreleased — 2026-11-19

- Add instrument conditions, candle history and existing operation status,
  completing the pinned contract's read-only REST operations.
- Validate candle timeframe, price type, count/range combinations and identifier
  path segments before network calls. Preserve signed negative candle counts.
- Model numeric bid/ask/both candles and exact Decimal instrument settings;
  distinguish terminal operation states from successful execution.
- Share new fixtures with Python and document the upstream candle oneOf ambiguity.
- Add a runnable market-data example. Live interoperability remains unverified.
