package main

import (
	_ "embed"
	"flag"
	"fmt"
	"io"
	"math"
	"math/rand"
	"os"
	"sort"
	"strings"
	"unicode/utf8"
)

//go:embed corpus.txt
var demoCorpus string

const maxCorpusBytes = 1 << 20

type vocabulary struct {
	characters []rune
	ids        map[rune]int
}

func newVocabulary(text string) vocabulary {
	v := vocabulary{ids: make(map[rune]int)}
	for _, r := range text {
		v.ids[r] = 0
	}
	for r := range v.ids {
		v.characters = append(v.characters, r)
	}
	sort.Slice(v.characters, func(i, j int) bool { return v.characters[i] < v.characters[j] })
	for i, r := range v.characters {
		v.ids[r] = i
	}
	return v
}
func (v vocabulary) encode(text string) ([]int, error) {
	tokens := make([]int, 0, len(text))
	for _, r := range text {
		id, ok := v.ids[r]
		if !ok {
			return nil, fmt.Errorf("prompt character %q is absent from the corpus", r)
		}
		tokens = append(tokens, id)
	}
	return tokens, nil
}
func readCorpus(reader io.Reader) (string, error) {
	data, err := io.ReadAll(io.LimitReader(reader, maxCorpusBytes+1))
	if err != nil {
		return "", err
	}
	if len(data) > maxCorpusBytes {
		return "", fmt.Errorf("corpus must be at most 1 MiB")
	}
	if !utf8.Valid(data) {
		return "", fmt.Errorf("corpus must be valid UTF-8")
	}
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	if strings.TrimSpace(text) == "" {
		return "", fmt.Errorf("corpus is empty")
	}
	return text, nil
}

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
}
func run(args []string, out, errOut io.Writer) error {
	flags := flag.NewFlagSet("day2", flag.ContinueOnError)
	flags.SetOutput(errOut)
	file := flags.String("file", "", "local UTF-8 training text (default: bundled demo)")
	steps := flags.Int("steps", 200, "training steps")
	seed := flags.Int64("seed", 42, "random seed")
	prompt := flags.String("prompt", "", "starting text (default: beginning of corpus)")
	generate := flags.Int("generate", 80, "number of new characters")
	context := flags.Int("context", 8, "context length, 1–32 characters")
	dim := flags.Int("dim", 8, "embedding dimension, 2–64, divisible by heads")
	heads := flags.Int("heads", 2, "parallel attention heads, 1–8")
	rate := flags.Float64("lr", 0.01, "Adam learning rate")
	if err := flags.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return err
	}
	if flags.NArg() != 0 || *steps < 0 || *steps > 10000 || *generate < 0 || *generate > 2000 || *context < 1 || *context > 32 || *dim < 2 || *dim > 64 || *heads < 1 || *heads > 8 || *dim%*heads != 0 || math.IsNaN(*rate) || math.IsInf(*rate, 0) || *rate <= 0 || *rate > 1 {
		return fmt.Errorf("invalid options; use -h to see limits (steps 0–10000, generate 0–2000, lr > 0 and <= 1)")
	}
	text := demoCorpus
	if *file != "" {
		f, err := os.Open(*file)
		if err != nil {
			return err
		}
		text, err = readCorpus(f)
		f.Close()
		if err != nil {
			return err
		}
	}
	vocab := newVocabulary(text)
	if len(vocab.characters) > 128 {
		return fmt.Errorf("keep the learning demo to 128 unique characters or fewer")
	}
	tokens, _ := vocab.encode(text)
	if len(tokens) <= *context {
		return fmt.Errorf("corpus needs more than %d characters", *context)
	}
	start := *prompt
	if start == "" {
		start = string([]rune(text)[:*context])
	}
	prefix, err := vocab.encode(start)
	if err != nil {
		return err
	}
	rng := rand.New(rand.NewSource(*seed))
	model, err := newTransformer(len(vocab.characters), *dim, *heads, *context, rng)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "Tiny decoder Transformer: %d parameters, %d characters, %d heads\n", len(model.parameters), len(vocab.characters), *heads)
	fmt.Fprintln(out, "Training from random weights on local text; no pretrained model or network access.")
	input, target := tokens[:*context], tokens[1:*context+1]
	fmt.Fprintf(out, "Initial training-window loss: %.4f\n", model.loss(input, target).data)
	optimizer := &adam{}
	for step := 1; step <= *steps; step++ {
		offset := rng.Intn(len(tokens) - *context)
		loss := model.loss(tokens[offset:offset+*context], tokens[offset+1:offset+*context+1])
		if math.IsNaN(loss.data) || math.IsInf(loss.data, 0) {
			return fmt.Errorf("non-finite loss; try a lower learning rate")
		}
		backward(loss)
		optimizer.update(model.parameters, *rate)
		if step == 1 || step%50 == 0 || step == *steps {
			fmt.Fprintf(out, "Step %d/%d: window loss %.4f\n", step, *steps, loss.data)
		}
	}
	fmt.Fprintf(out, "Final training-window loss: %.4f\n", model.loss(input, target).data)
	fmt.Fprintln(out, "Sample (a tiny model may repeat itself or produce nonsense):")
	if _, err := io.WriteString(out, start); err != nil {
		return err
	}
	for i := 0; i < *generate; i++ {
		window := prefix
		if len(window) > model.context {
			window = window[len(window)-model.context:]
		}
		logits := model.forward(window)
		probabilities := softmax(logits[len(logits)-1])
		choice, draw := len(probabilities)-1, rng.Float64()
		for j, p := range probabilities {
			draw -= p.data
			if draw <= 0 {
				choice = j
				break
			}
		}
		prefix = append(prefix, choice)
		if _, err := io.WriteString(out, string(vocab.characters[choice])); err != nil {
			return err
		}
	}
	_, err = fmt.Fprintln(out)
	return err
}
