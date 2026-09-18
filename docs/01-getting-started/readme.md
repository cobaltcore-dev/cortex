<!--
# SPDX-FileCopyrightText: Copyright SAP SE or an SAP affiliate company and cobaltcore-dev contributors
#
# SPDX-License-Identifier: Apache-2.0
-->

# Chapter 1 — Getting started

This chapter builds the mental model you need for the rest of the book, then walks through the
machinery a developer or operator touches on day one. It assumes no prior knowledge of Cortex.

We start with *what Cortex is and why it exists*, then look at how the pieces fit together
architecturally. From there the chapter turns practical: how Cortex is built and shipped (CI/CD and
the container images), how it is packaged (the Helm charts), how you build and test it locally (the
Make targets), where its data lives (Postgres and the developer tooling), and finally how to run the
whole thing on your laptop with Tilt. It closes with installing a real domain bundle into a cluster.

By the end you will understand the shape of the system and be able to stand up a local Cortex and
watch a code change reload.

## In this chapter

1. [What is Cortex?](01-what-is-cortex.md) — the problem it solves and the three components.
2. [Architecture at a glance](02-architecture-at-a-glance.md) — the modular monolith, the CRD/controller model, and "advise, don't replace".
3. [CI/CD and packaging](03-cicd-and-packaging.md) — how images are built and charts are released.
4. [The Helm charts](04-helm-charts.md) — the three-tier chart layout and how a deployment is rendered.
5. [Make targets](05-make-targets.md) — building, generating, linting, and testing.
6. [Postgres and tooling](06-postgres-and-tooling.md) — the datastore and the `tools/` folder.
7. [Local development with Tilt](07-local-development-with-tilt.md) — a hands-on first run.
8. [Install a domain bundle](08-installing-a-domain-bundle.md) — deploying a domain into a real cluster.

## Next

[Prev: The Cortex Book](../readme.md) · [Next: What is Cortex? »](01-what-is-cortex.md)
