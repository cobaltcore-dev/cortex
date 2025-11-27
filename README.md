<!--
# SPDX-FileCopyrightText: Copyright 2024 SAP SE or an SAP affiliate company and cobaltcore-dev contributors
#
# SPDX-License-Identifier: Apache-2.0
-->

Cortex
======
[![REUSE status](https://api.reuse.software/badge/github.com/cobaltcore-dev/cortex)](https://api.reuse.software/info/github.com/cobaltcore-dev/cortex)
<a href="https://github.com/cobaltcore-dev/cortex"><img align="left" width="190" height="190" src="./docs/assets/Cortex_Logo_black_space_square_bg_rd@2x.png"></a>

Cortex is a modular and extensible service for initial placement and scheduling in cloud-native environments covering workloads such as compute, storage, network, and other scheduling domains.  
It improves resource utilization and operational performance by making smart placement decisions based on the current state of the environment and defined constraints and objectives.

As part of the CobaltCore project, it complements the platform with advanced placement and scheduling capabilities.  

Learn more about [CobaltCore](https://cobaltcore-dev.github.io/docs/) and the broader [Apeiro Reference Architecture](https://apeirora.eu) ecosystem.

## Features

- **Modular and extensible design**  
  Cortex consists of a minimal core framework that can be extended with various plugins to support different data sources and scheduling algorithms. 
  This provides flexibility and enables adapting Cortex to various environments and requirements.

- **Centralized knowledge database**
  Cortex provides a holistic knowledge database that stores enriched data from various sources.
  This enables efficient and consistent access to the infrastructure state for placement and scheduling decisions.

- **Integrated placement and scheduling**  
  Cortex combines initial placement and continuous scheduling into a single service.

- **Cross-domain support**  
  Cortex supports a wide range of workloads from various scheduling domains, including compute, storage, network, and Kubernetes. 
  The architecture allows handling the domains either independently or through coordinated multi-domain decisions.

- **Performance and scalability**
  Cortex is designed for production-scale deployments using algorithmic approaches to balance decision quality, execution efficiency, and maintaining a low resource footprint.
  It is battle-tested in large-scale, production cloud computing environments and can handle thousands of placement requests per second.

## Documentation

Read the full documentation at [docs/readme.md](docs/readme.md).

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

<p align="center">
  <img alt="Bundesministerium für Wirtschaft und Energie (BMWE)-EU funding logo" src="https://apeirora.eu/assets/img/BMWK-EU.png" width="400"/>
</p>
