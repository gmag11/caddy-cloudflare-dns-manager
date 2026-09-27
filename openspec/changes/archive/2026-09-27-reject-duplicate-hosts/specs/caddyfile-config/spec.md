# Spec Delta

## ADDED Requirements

### Requirement: Unique host declarations

The plugin SHALL require that a given hostname be declared by at most one per-site `cf_dns_manager` directive. Host identity SHALL be compared on the normalized hostname (case-insensitive, trailing dot ignored), so a literal FQDN and a named matcher that resolves to the same FQDN identify the same host. When two or more directives declare the same host, loading and processing the configuration SHALL fail with an error naming the duplicated host, and the plugin SHALL NOT register or reconcile either declaration.

#### Scenario: Same literal FQDN declared twice

- **WHEN** two `cf_dns_manager` directives each declare `host foo.example.com`
- **THEN** loading the configuration fails with an error identifying `foo.example.com` as declared more than once

#### Scenario: Matcher and literal resolve to the same FQDN

- **WHEN** one directive declares `host foo.example.com` and another declares `host @foo` where the `@foo` matcher resolves to `foo.example.com`
- **THEN** loading the configuration fails with the duplicate-host error

#### Scenario: Equivalent spellings collide

- **WHEN** two directives declare the same host written differently, such as `Foo.Example.com.` and `foo.example.com`
- **THEN** loading the configuration fails with the duplicate-host error

#### Scenario: Duplicate across host modes

- **WHEN** one directive declares `host foo.example.com` as an address host and another declares the same hostname as a tunnel host
- **THEN** loading the configuration fails with the duplicate-host error, because the collision is on the hostname regardless of how it is served

#### Scenario: Distinct hosts are unaffected

- **WHEN** several directives each declare a distinct hostname
- **THEN** the configuration loads successfully and every host is registered
