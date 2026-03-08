# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- Core pipeline architecture (`review` and `reply`) for processing GitLab MRs.
- Interactive CLI agent to trigger and selectively push MR comments.
- Asynchronous queuing system (`internal/pkg/queue`) with retry thresholds.
- Modular LLM support for OpenAI, Anthropic, and Google with token load-balancing.
- Multi-storage driver abstraction (`file`, `sqlite`, `postgres`).
- Periodic feedback consolidator Cron logic to extract learned project conventions from recurring feedbacks.
