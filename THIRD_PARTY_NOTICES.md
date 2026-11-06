# Third-party notices

## T3 Code

The interaction and Codex integration reference is [T3 Code](https://github.com/pingdotgg/t3code), by T3 Tools Inc. MujicAgent implements its native UI and application code independently in Go.

`internal/view/assets/openai.svg` is the OpenAI provider icon from T3 Code's [`apps/marketing/public/harnesses/openai_dark.svg`](https://github.com/pingdotgg/t3code/blob/main/apps/marketing/public/harnesses/openai_dark.svg). The OpenAI name and logo belong to OpenAI; the icon identifies the model provider and does not imply endorsement.

```text
MIT License

Copyright (c) 2026 T3 Tools Inc.

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
```

## Lucide

The SVGs in `internal/view/assets/lucide/` are unmodified assets from [Lucide](https://lucide.dev/), pinned to [commit 1a49fd88d85202bad69f39462241b309a484bba0](https://github.com/lucide-icons/lucide/tree/1a49fd88d85202bad69f39462241b309a484bba0/icons). They use the original 24 × 24 viewBox, 2-unit strokes, and rounded caps and joins, scaled by the native renderer.

The complete upstream [ISC license and Feather-derived MIT notice](internal/view/assets/lucide/LICENSE) are preserved alongside the SVGs and embedded in the application. No JavaScript icon package is required.

## Dependencies

MyGo and the transitive Go modules retain their respective licenses in their module distributions. `go.mod` and `go.sum` pin the versions used to build MujicAgent. Codex CLI is an external executable, installed separately by the user.
