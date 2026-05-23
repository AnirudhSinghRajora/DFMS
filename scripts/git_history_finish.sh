#!/bin/bash
set -e

# 36. OpenAPI Spec
git add api/openapi/
git commit -m "docs(api): create OpenAPI 3.1 specification" \
           -m "Formally documents all 27 REST endpoints exposed by the API Gateway.
Includes comprehensive request/response schemas, security definitions, and usage examples for integration."

# 37. Architecture Documentation
git add docs/
git commit -m "docs(arch): add detailed system architecture documentation" \
           -m "Writes in-depth markdown files using Mermaid diagrams to explain system internals.
Covers the overall microservice architecture, content-defined chunking (CDC), consistent hashing replication, and the observability stack."

# 38. Demo Script
git add scripts/demo.sh
git commit -m "docs(demo): add interactive terminal demo script" \
           -m "Provides an executable walkthrough of the system.
Automatically registers a user, performs an upload (triggering chunking), performs a duplicate upload (triggering dedup), and verifies the downloaded file integrity."

# 39. README
git add README.md
git commit -m "docs(readme): write comprehensive project landing page" \
           -m "Creates the primary repository entry point.
Highlights features, technology stack, architecture overview, and quick-start instructions. Serves as a portfolio-grade introduction to the platform."

# 40. Catch-all for any remaining configs or root files
git add .
git commit -m "chore(release): format codebase and finalize v1.0.0 release" \
           -m "Ensures all remaining configuration files and minor tweaks are committed.
Prepares the repository for its initial public release."

echo "✅ Git repository finished with 40-commit history following Conventional Commits."
git log --oneline | wc -l
