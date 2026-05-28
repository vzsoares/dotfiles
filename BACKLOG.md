# zen-release — backlog

Future work for `scripts/release.py` (`zen-release`). The architecture and design
live in `docs/wiki/architecture/release-tooling.md`; usage is in `scripts/README.md`.

## Future work

- [ ] Live smoke test of a real `npm` / `uv` publish (only mocked so far) — pair
      with the next real package release.
- [ ] Monorepo / multiple packages per repo.
- [ ] Signing tags/artifacts (GPG, cosign).
