<!--
# SPDX-FileCopyrightText: Copyright 2024 SAP SE or an SAP affiliate company and cobaltcore-dev contributors
#
# SPDX-License-Identifier: Apache-2.0
-->

[![REUSE status](https://api.reuse.software/badge/github.com/cobaltcore-dev/cortex)](https://api.reuse.software/info/github.com/cobaltcore-dev/cortex)

# Cortex

Cortex is an advanced initial placement and scheduling service for compute, storage, and network workloads within OpenStack cloud environments.  

**Idea**: Chainable filter and weigher pipeline, leveraging heuristic-based decision-making to optimize placement and scheduling.

## Features

- **Unified Placement and Scheduling**: Combines initial placement and scheduling (re-balancing) within a single, efficient framework.

- **Minimal Resource Footprint**: Designed to operate with low resource consumption, optimizing performance.

- **High Scalability**: Validated in large-scale, production environments with compute workload up to 50,000 VMs and 2,000 hypervisors.

- **Straightforward Integration**: Connects the initial placement component to the respective message queue and uses OpenStack APIs for scheduling.

- **Extensible Architecture**: Chainable concept for filters and weighers, allowing flexible customization.

- **Knowledge Database**: Stores and retrieves enriched environmental data, enabling intelligent decision-making.

## Documentation

- **Users** can find more information on the ideas and concepts behind cortex in [the feature docs](./docs/features.md) and [the architecture docs](./docs/architecture.md).
- **Developers** can find a guide on how to develop with cortex in [the development docs](./docs/develop.md).

## Roadmap

See our [roadmap](https://github.com/orgs/cobaltcore-dev/projects/14) and [issue tracker](https://github.com/cobaltcore-dev/cortex/issues) for upcoming features and improvements.

## Support, Feedback, Contributing

This project is open to feature requests/suggestions, bug reports etc. via [GitHub issues](https://github.com/cobaltcore-dev/cortex/issues). Contribution and feedback are encouraged and always welcome. For more information about how to contribute, the project structure, as well as additional contribution information, see our [Contribution Guidelines](CONTRIBUTING.md).

## Security / Disclosure
If you find any bug that may be a security problem, please follow our instructions at [in our security policy](https://github.com/SAP/<your-project>/security/policy) on how to report it. Please do not create GitHub issues for security-related doubts or problems.

## Code of Conduct

We as members, contributors, and leaders pledge to make participation in our community a harassment-free experience for everyone. By participating in this project, you agree to abide by its [Code of Conduct](https://github.com/SAP/.github/blob/main/CODE_OF_CONDUCT.md) at all times.

## Licensing

Copyright 2024-2025 SAP SE. Please see our [LICENSE](LICENSE) for copyright and license information. Detailed information including third-party components and their licensing/copyright information is available [via the REUSE tool](https://api.reuse.software/info/github.com/cobaltcore-dev/cortex).
