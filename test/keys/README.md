# Test Keys

Generate before first test run:

    just generate-test-keys

This creates:
- `test_id_ed25519` — private key (mode 0600, gitignored)
- `test_id_ed25519.pub` — public key (committed, baked into Packer image)

The public key file must exist before running `just build-base-image`.
