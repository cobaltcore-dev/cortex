<!--
# SPDX-FileCopyrightText: Copyright 2024 SAP SE or an SAP affiliate company and cobaltcore-dev contributors
#
# SPDX-License-Identifier: Apache-2.0
-->

[![REUSE status](https://api.reuse.software/badge/github.com/cobaltcore-dev/cortex)](https://api.reuse.software/info/github.com/cobaltcore-dev/cortex)

# Cortex

Cortex is a smart placement service for virtual machines (VMs) in an [OpenStack](https://www.openstack.org/) cloud environment. It is designed to improve resource usage in a data center by making smart(er) decisions about where to place VMs.

## Features

- **Data sync:** Flexible framework to sync metrics and placement information of a data center.
- **Knowledge extraction**: Logic to extract simple or advanced knowledge ("features") from the synced data.
- **Smart scheduling:** Scheduling pipeline for VMs based on the extracted knowledge.

## Quickstart

Copy the example secrets values file and insert your credentials.
```bash
cp cortex.secrets.example.yaml "${HOME}/cortex.secrets.yaml"
```

> [!WARNING]
> It is recommended to put the secrets file somewhere outside of the project directory, as it contains sensitive information. In this way, it won't be accidentally committed to the repository.

Tell tilt where to find your secrets file:
```bash
export TILT_VALUES_PATH="${HOME}/cortex.secrets.yaml"
```

Run the tilt setup:
```bash
minikube start && tilt up
```

Point your browser to http://localhost:10350/

## Support, Feedback, Contributing

This project is open to feature requests/suggestions, bug reports etc. via [GitHub issues](https://github.com/cobaltcore-dev/cortex/issues). Contribution and feedback are encouraged and always welcome. For more information about how to contribute, the project structure, as well as additional contribution information, see our [Contribution Guidelines](CONTRIBUTING.md).

## Security / Disclosure
If you find any bug that may be a security problem, please follow our instructions at [in our security policy](https://github.com/SAP/<your-project>/security/policy) on how to report it. Please do not create GitHub issues for security-related doubts or problems.

## Code of Conduct

We as members, contributors, and leaders pledge to make participation in our community a harassment-free experience for everyone. By participating in this project, you agree to abide by its [Code of Conduct](https://github.com/SAP/.github/blob/main/CODE_OF_CONDUCT.md) at all times.

## Licensing

Copyright 2024-2025 SAP SE. Please see our [LICENSE](LICENSE) for copyright and license information. Detailed information including third-party components and their licensing/copyright information is available [via the REUSE tool](https://api.reuse.software/info/github.com/cobaltcore-dev/cortex).
