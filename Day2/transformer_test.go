package main

import (
	"bytes"
	"io"
	"math"
	"math/rand"
	"strings"
	"testing"
)

func TestStableSoftmaxAndCrossEntropy(t *testing.T) {
	logits := []*value{scalar(1000), scalar(1001), scalar(-1000)}
	probabilities := softmax(logits)
	sum := 0.0
	for _, p := range probabilities {
		sum += p.data
	}
	if math.Abs(sum-1) > 1e-12 {
		t.Fatal("probabilities do not sum to one")
	}
	loss := crossEntropy(logits, 2)
	backward(loss)
	if math.IsNaN(loss.data) || math.IsInf(loss.data, 0) {
		t.Fatal("unstable cross entropy")
	}
	for i, p := range logits {
		want := probabilities[i].data
		if i == 2 {
			want--
		}
		if math.Abs(p.grad-want) > 1e-12 {
			t.Fatal("incorrect gradient")
		}
	}
	// Check the off-diagonal softmax derivative separately.
	backward(probabilities[0])
	if math.Abs(logits[1].grad+probabilities[0].data*probabilities[1].data) > 1e-12 {
		t.Fatal("missing softmax cross term")
	}
}

func TestSharedNodeGradient(t *testing.T) {
	x := scalar(3)
	square := mul(x, x)
	loss := add(square, square)
	backward(loss)
	if x.grad != 12 {
		t.Fatalf("shared graph gradient: got %g want 12", x.grad)
	}
	backward(loss)
	if x.grad != 12 {
		t.Fatal("gradients accumulated between backward calls")
	}
}

func TestTransformerGradients(t *testing.T) {
	m, err := newTransformer(3, 4, 2, 3, rand.New(rand.NewSource(7)))
	if err != nil {
		t.Fatal(err)
	}
	input, target := []int{0, 1, 2}, []int{1, 2, 0}
	loss := m.loss(input, target)
	backward(loss)
	// Compare analytic gradients against central finite differences throughout
	// the parameter list: embeddings, attention, feed-forward, output and norms.
	for i := 0; i < len(m.parameters); i += 7 {
		p := m.parameters[i]
		original, analytic := p.data, p.grad
		const eps = 1e-5
		p.data = original + eps
		plus := m.loss(input, target).data
		p.data = original - eps
		minus := m.loss(input, target).data
		p.data = original
		numeric := (plus - minus) / (2 * eps)
		if math.Abs(analytic-numeric) > 1e-5*(1+math.Abs(numeric)) {
			t.Fatalf("parameter %d: analytic %g, numeric %g", i, analytic, numeric)
		}
	}
}

func TestCausalMask(t *testing.T) {
	m, _ := newTransformer(4, 4, 2, 4, rand.New(rand.NewSource(1)))
	first := m.forward([]int{0, 1, 2, 3})
	changed := m.forward([]int{0, 1, 3, 2})
	for pos := 0; pos < 2; pos++ {
		for token := 0; token < 4; token++ {
			if first[pos][token].data != changed[pos][token].data {
				t.Fatal("future token affected earlier prediction")
			}
		}
	}
	prefix := m.forward([]int{0, 1})
	for token := 0; token < 4; token++ {
		if prefix[1][token].data != first[1][token].data {
			t.Fatal("prefix behavior differs")
		}
	}
}

func TestTrainingLearns(t *testing.T) {
	m, _ := newTransformer(2, 4, 2, 4, rand.New(rand.NewSource(4)))
	input, target := []int{0, 1, 0, 1}, []int{1, 0, 1, 0}
	initial := m.loss(input, target).data
	optimizer := &adam{}
	for i := 0; i < 60; i++ {
		loss := m.loss(input, target)
		backward(loss)
		optimizer.update(m.parameters, 0.02)
	}
	final := m.loss(input, target).data
	if final >= initial*0.3 {
		t.Fatalf("loss did not improve enough: %g -> %g", initial, final)
	}
	for _, p := range m.parameters {
		if p.grad != 0 {
			t.Fatal("optimizer left stale gradients")
		}
	}
}

func TestInputAndDeterminism(t *testing.T) {
	v := newVocabulary("go 猫🙂")
	encoded, err := v.encode("猫🙂go")
	if err != nil {
		t.Fatal(err)
	}
	var decoded strings.Builder
	for _, id := range encoded {
		decoded.WriteRune(v.characters[id])
	}
	if decoded.String() != "猫🙂go" {
		t.Fatal("rune tokenizer failed")
	}
	if _, err := v.encode("x"); err == nil {
		t.Fatal("unknown prompt character accepted")
	}
	for _, bad := range []string{" \n", string([]byte{255}), strings.Repeat("a", maxCorpusBytes+1)} {
		if _, err := readCorpus(strings.NewReader(bad)); err == nil {
			t.Fatal("invalid corpus accepted")
		}
	}
	text, err := readCorpus(strings.NewReader("a\r\nb"))
	if err != nil || text != "a\nb" {
		t.Fatal("newline normalization failed")
	}
	var a, b bytes.Buffer
	args := []string{"-steps", "2", "-generate", "4", "-seed", "12"}
	if err := run(args, &a, io.Discard); err != nil {
		t.Fatal(err)
	}
	if err := run(args, &b, io.Discard); err != nil {
		t.Fatal(err)
	}
	if a.String() != b.String() {
		t.Fatal("fixed seed is not deterministic")
	}
	for _, args := range [][]string{{"-heads", "0"}, {"-dim", "7", "-heads", "2"}, {"-lr", "NaN"}, {"-context", "0"}, {"-prompt", "🦖"}} {
		if err := run(args, io.Discard, io.Discard); err == nil {
			t.Fatalf("invalid args accepted: %v", args)
		}
	}
}
