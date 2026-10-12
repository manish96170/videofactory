# Phase 17 — Formal Review: Terra

**Model:** `amazon-bedrock/openai.gpt-5.6-terra#high` · **Date:** 2026-10-12.

Terra found HIGH build path/VCS metadata exposure plus MEDIUM profile-output and stale
documentation issues. Commands now use `-trimpath -buildvcs=false` and portable mktemp
output paths; version smoke invokes the explicit temporary binary. Terra confirmed all
findings resolved.

**Final status: APPROVE.**
