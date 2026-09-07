# Day 2: A tiny Transformer from scratch

A trainable, character-level decoder Transformer in Go, using only the standard library. It learns to predict the next character in a local text corpus. Embeddings, attention projections, feed-forward layers, layer norms, and the output layer are all trained through our own automatic differentiation engine.

This is an educational model, not a pretrained chatbot. The default model has 939 parameters and will produce imperfect, often nonsensical text. Training from scratch is the point of this project.

## Run

Requires Go 1.24 or newer. From the repository root:

```sh
cd Day2
go run .
```

The default run trains for 200 steps on `corpus.txt`, then generates 80 new characters. It exits afterward. It does not start a server, access the internet, or write files.

Try a longer run:

```sh
go run . -steps 2000 -prompt "go " -generate 120
```

Train on your own local UTF-8 file:

```sh
go run . -file ./my-text.txt -steps 1000 -prompt "hello"
```

Every character in the prompt must occur in the training text. Omit `-prompt` to use the beginning of the corpus. Files are limited to 1 MiB and 128 unique characters to keep the demo manageable. The corpus must contain at least two unique characters and be longer than the context window.

### Options

| Flag | Default | Meaning |
| --- | --- | --- |
| `-file` | bundled corpus | Local training text |
| `-steps` | 200 | Adam updates; 0 skips training |
| `-context` | 8 | Maximum preceding characters, 1–32 |
| `-dim` | 8 | Embedding dimension, 2–64 |
| `-heads` | 2 | Independent attention heads, 1–8; must divide dimension |
| `-lr` | 0.01 | Learning rate |
| `-seed` | 42 | Seed for initialization, windows, and sampling |
| `-prompt` | beginning of corpus | Starting text |
| `-generate` | 80 | New characters to sample |

Larger dimensions and contexts take more memory and CPU. Repeating a run with the same inputs and seed is deterministic on the same setup. Changing the step count also changes the random sampling state.

## What is implemented

The model has one decoder block with this structure:

```text
character IDs
  -> learned embeddings + sinusoidal positions
  -> layer norm -> causal multi-head attention -> residual addition
  -> layer norm -> linear -> ReLU -> linear -> residual addition
  -> final layer norm -> vocabulary logits
```

Each attention head computes `softmax(QKᵀ / sqrt(head dimension))V`. At position `t`, attention considers only positions `0` through `t`; it cannot see the character it is supposed to predict. Head results are concatenated and projected back to the embedding dimension.

For a window such as `go makes`, inputs are those characters and targets are the same sequence shifted one character forward in the corpus. The loss is average next-character cross entropy. Backpropagation computes gradients for learned weights; Adam applies an update with global gradient norm clipping. Positional encodings are fixed, not learned.

Generation takes the last context window, predicts one character, samples it, appends it, and repeats. Positions restart at zero for each sliding window.

### Why goroutines?

Attention heads are independent during the forward pass. Each head builds its graph in its own goroutine and writes to its own result slice. A `sync.WaitGroup` joins them before concatenation. Backpropagation and Adam run serially so shared parameter gradients are not updated concurrently.

For a model this small, goroutine overhead may outweigh any speedup. This demonstrates a valid concurrency boundary rather than claiming improved performance.

### Files to read in order

1. `main.go`: rune vocabulary, bounded file reading with `io`, text handling with `strings`, training loop, and generation.
2. `autograd.go`: scalar computation graph, derivatives, stable softmax/cross entropy, and reverse-mode backpropagation.
3. `transformer.go`: linear layers, layer normalization, attention, residual connections, and Adam.
4. `transformer_test.go`: numerical gradient checks, causal masking, learning, input handling, and determinism.

## Verification

```sh
go test -race ./...
go vet ./...
```

Tests compare analytic gradients with finite differences across the model, check that changing future tokens cannot affect earlier logits, and verify loss reduction on a simple alternating-character sequence.

In the default 200-step run tested during development, loss on the fixed first training window went from **2.7211 to 1.3474**. This is training loss, not a held-out evaluation or a claim about generalization. Individual logged steps use randomly selected windows, so their losses need not decrease monotonically.

## Deliberate limits

- One block; character tokens; one window per optimizer update.
- No pretrained weights, GPU libraries, external dependencies, or downloads.
- No model checkpoints: every invocation starts fresh.
- No validation split or language-quality benchmark.
- Scalar graph operations favor readable derivatives over performance; generation also builds graphs, although it does not backpropagate.
- No dropout, KV cache, batching, or production inference optimizations.

Possible next exercises: save/load weights, add a held-out evaluation split, then replace scalar operations with matrix operations while preserving the gradient tests.
