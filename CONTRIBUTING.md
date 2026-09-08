# Contributing

Thanks for helping improve Visual Tmux Client.

## Development setup

Install Go 1.26.3+, Node.js 24+, npm, and tmux 3.3+. Then run:

```sh
make test
make build
./hub/visual-tmux-client
```

The browser UI is under `hub/web`. For frontend development with hot reload, run `npm run dev` there. API requests still need a running Go server or an appropriate local proxy.

## Pull requests

- Keep each change focused and explain its user-visible impact.
- Add or update tests for behavior changes.
- Run `make test` before submitting.
- Never commit tokens, `.env` files, `.mcp.json`, or other machine-local configuration.
- Update the README or OpenSpec specifications when public behavior changes.

By contributing, you agree that your contribution is licensed under the MIT License.
