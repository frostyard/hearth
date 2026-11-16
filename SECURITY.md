# Security Policy

## Supported versions

hearth is pre-release. Only the latest `v0` tag and the `main` branch get
fixes. An older tag gets no fix; move to the latest release.

## Reporting a vulnerability

Use GitHub private vulnerability reporting:
https://github.com/frostyard/hearth/security

Open the Security tab and click **Report a vulnerability**.

Do not open a public issue with the details of a vulnerability. Do not post
credentials in an issue, a pull request or a discussion.

## What to include

- The affected commit or tag.
- The impact: what an attacker can do.
- The steps to reproduce it.

## What hearth holds

hearth generates code and 1 JSON file from Protobuf definitions; it runs no
network service and holds no credential. A problem in a generated permission
check, in the manifest format or in the options' extension numbers is still a
security report: send it through the private route above.
