# Built-in rule bundles

Empty for now, and deliberately so.

A bundle here would be YAML that nothing in the repository can validate until the
rule engine lands. Shipping an unvalidated bundle would mean shipping a file that
reads like configuration, gets loaded by `securityctl rules lint` once the engine
exists, and is therefore already trusted before it has ever been checked. The
built-in bundle has to be the one that passes `lint` and a reviewed `simulate`
diff like any customer bundle, not a special case that skipped both.

The schema the engine will compile against is at
`contracts/json-schema/rule-bundle.schema.json`. Two properties of it are load
bearing for the engine that arrives with Track D:

- There is no regex operator. The strongest form is `contains_prefix`. An
  unbounded pattern in a bundle an operator uploads is a denial-of-service
  delivered as a YAML file.
- Any condition that participates in a decision on a Tier C signal must carry
  `corroborate.ifAIAvailable` with an explicit `degradeTo`. A missing AI
  observation is never a negative observation, and the schema is where that is
  enforced rather than merely documented.

When the first bundle lands it goes here, and the `rules` job in
`.github/workflows/ci.yml` — currently gated off with `if: false` and a note
explaining why — comes back with it.
