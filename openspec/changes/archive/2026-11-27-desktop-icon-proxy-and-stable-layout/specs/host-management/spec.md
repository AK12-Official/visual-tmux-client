# Spec Delta

## ADDED Requirements

### Requirement: Proxy-related connection failure reporting

A remote connection that fails because of the configured proxy SHALL be reported as a host error that distinguishes the proxy being unreachable, the proxy refusing the connection and the target host not answering through the proxy. These SHALL be distinct from authentication, trust and direct network failures. Host status SHALL name whether a proxy or a direct connection was used.

#### Scenario: Proxy is unreachable

- **WHEN** the configured proxy is not accepting connections and a remote host is tested or used
- **THEN** that host reports the proxy as unreachable, remains registered, and the local host and the central login stay unaffected

#### Scenario: Target does not answer through the proxy

- **WHEN** the proxy accepts the connection but the target host does not answer
- **THEN** the error names the target host as unreachable through the proxy and does not report the proxy as failed

#### Scenario: Proxy refuses the connection

- **WHEN** the proxy rejects the connection to the target host
- **THEN** the error reports that the proxy refused the connection

#### Scenario: Status names the connection path

- **WHEN** a remote host's status is observed
- **THEN** it names the configured proxy address as the connection path, or names a direct connection when no proxy is set

#### Scenario: Trust failures are not reported as proxy failures

- **WHEN** a remote host's server key is unconfirmed or has changed while connecting through a proxy
- **THEN** the existing trust errors are reported unchanged
