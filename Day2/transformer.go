package main

import (
	"fmt"
	"math"
	"math/rand"
	"sync"
)

type linear struct {
	weights [][]*value
	bias    []*value
}

func (l linear) apply(x []*value) []*value {
	result := make([]*value, len(l.weights))
	for i, row := range l.weights {
		sum := l.bias[i]
		for j, w := range row {
			sum = add(sum, mul(w, x[j]))
		}
		result[i] = sum
	}
	return result
}

type layerNorm struct{ gain, bias []*value }

func (n layerNorm) apply(x []*value) []*value {
	mean := scalar(0)
	for _, v := range x {
		mean = add(mean, v)
	}
	mean = scale(mean, 1/float64(len(x)))
	centered := make([]*value, len(x))
	variance := scalar(0)
	for i, v := range x {
		centered[i] = add(v, scale(mean, -1))
		variance = add(variance, mul(centered[i], centered[i]))
	}
	invStd := power(add(scale(variance, 1/float64(len(x))), scalar(1e-5)), -0.5)
	result := make([]*value, len(x))
	for i, v := range centered {
		result[i] = add(mul(mul(v, invStd), n.gain[i]), n.bias[i])
	}
	return result
}

type attentionHead struct{ query, key, value linear }
type transformer struct {
	dim, context                         int
	embedding                            [][]*value
	heads                                []attentionHead
	projection, expand, contract, output linear
	norm1, norm2, finalNorm              layerNorm
	parameters                           []*value
}

func newTransformer(vocab, dim, heads, context int, rng *rand.Rand) (*transformer, error) {
	if vocab < 2 || dim < 2 || heads < 1 || heads > 8 || dim%heads != 0 || context < 1 {
		return nil, fmt.Errorf("need vocabulary >= 2, dimension >= 2 divisible by 1–8 heads, and positive context")
	}
	m := &transformer{dim: dim, context: context}
	parameter := func(x float64) *value { v := scalar(x); m.parameters = append(m.parameters, v); return v }
	matrix := func(rows, columns int) [][]*value {
		result := make([][]*value, rows)
		for i := range result {
			result[i] = make([]*value, columns)
			for j := range result[i] {
				result[i][j] = parameter(rng.NormFloat64() / math.Sqrt(float64(columns)))
			}
		}
		return result
	}
	dense := func(in, out int) linear {
		l := linear{weights: matrix(out, in), bias: make([]*value, out)}
		for i := range l.bias {
			l.bias[i] = parameter(0)
		}
		return l
	}
	norm := func() layerNorm {
		n := layerNorm{gain: make([]*value, dim), bias: make([]*value, dim)}
		for i := 0; i < dim; i++ {
			n.gain[i] = parameter(1)
			n.bias[i] = parameter(0)
		}
		return n
	}
	m.embedding = matrix(vocab, dim)
	m.heads = make([]attentionHead, heads)
	for i := range m.heads {
		m.heads[i] = attentionHead{dense(dim, dim/heads), dense(dim, dim/heads), dense(dim, dim/heads)}
	}
	m.projection = dense(dim, dim)
	m.expand = dense(dim, dim*2)
	m.contract = dense(dim*2, dim)
	m.output = dense(dim, vocab)
	m.norm1 = norm()
	m.norm2 = norm()
	m.finalNorm = norm()
	return m, nil
}

// attend implements softmax(QKᵀ / sqrt(head dimension))V. Only positions <= t
// exist in the score vector, so future tokens have exactly zero influence.
func (h attentionHead) attend(x [][]*value) [][]*value {
	length := len(x)
	queries, keys, values := make([][]*value, length), make([][]*value, length), make([][]*value, length)
	for t := range x {
		queries[t] = h.query.apply(x[t])
		keys[t] = h.key.apply(x[t])
		values[t] = h.value.apply(x[t])
	}
	width := len(queries[0])
	result := make([][]*value, length)
	for t := range x {
		scores := make([]*value, t+1)
		for j := 0; j <= t; j++ {
			dot := scalar(0)
			for k := 0; k < width; k++ {
				dot = add(dot, mul(queries[t][k], keys[j][k]))
			}
			scores[j] = scale(dot, 1/math.Sqrt(float64(width)))
		}
		weights := softmax(scores)
		result[t] = make([]*value, width)
		for k := 0; k < width; k++ {
			sum := scalar(0)
			for j, w := range weights {
				sum = add(sum, mul(w, values[j][k]))
			}
			result[t][k] = sum
		}
	}
	return result
}

func (m *transformer) forward(tokens []int) [][]*value {
	x := make([][]*value, len(tokens))
	normalized := make([][]*value, len(tokens))
	for t, token := range tokens {
		x[t] = make([]*value, m.dim)
		for d := 0; d < m.dim; d++ {
			angle := float64(t) / math.Pow(10000, float64(2*(d/2))/float64(m.dim))
			position := math.Sin(angle)
			if d%2 == 1 {
				position = math.Cos(angle)
			}
			x[t][d] = add(m.embedding[token][d], scalar(position))
		}
		normalized[t] = m.norm1.apply(x[t])
	}
	// Each goroutine writes a separate slice. Parameters are read-only here;
	// gradients and optimizer updates happen after all heads have joined.
	results := make([][][]*value, len(m.heads))
	var wg sync.WaitGroup
	for i, h := range m.heads {
		wg.Add(1)
		go func() { defer wg.Done(); results[i] = h.attend(normalized) }()
	}
	wg.Wait()
	logits := make([][]*value, len(tokens))
	for t := range tokens {
		joined := make([]*value, 0, m.dim)
		for _, head := range results {
			joined = append(joined, head[t]...)
		}
		attention := m.projection.apply(joined)
		residual := make([]*value, m.dim)
		for d := range residual {
			residual[d] = add(x[t][d], attention[d])
		}
		hidden := m.expand.apply(m.norm2.apply(residual))
		for i := range hidden {
			hidden[i] = relu(hidden[i])
		}
		feedforward := m.contract.apply(hidden)
		for d := range residual {
			residual[d] = add(residual[d], feedforward[d])
		}
		logits[t] = m.output.apply(m.finalNorm.apply(residual))
	}
	return logits
}

func (m *transformer) loss(input, target []int) *value {
	logits := m.forward(input)
	total := scalar(0)
	for i, row := range logits {
		total = add(total, crossEntropy(row, target[i]))
	}
	return scale(total, 1/float64(len(input)))
}

type adam struct {
	first, second []float64
	step          int
}

func (a *adam) update(parameters []*value, rate float64) {
	if a.first == nil {
		a.first = make([]float64, len(parameters))
		a.second = make([]float64, len(parameters))
	}
	a.step++
	// Global norm clipping reduces unstable updates from a single training window.
	norm := 0.0
	for _, p := range parameters {
		norm += p.grad * p.grad
	}
	clip := 1.0
	if norm > 1 {
		clip = 1 / math.Sqrt(norm)
	}
	for i, p := range parameters {
		g := p.grad * clip
		a.first[i] = 0.9*a.first[i] + 0.1*g
		a.second[i] = 0.999*a.second[i] + 0.001*g*g
		first := a.first[i] / (1 - math.Pow(0.9, float64(a.step)))
		second := a.second[i] / (1 - math.Pow(0.999, float64(a.step)))
		p.data -= rate * first / (math.Sqrt(second) + 1e-8)
		// Unused embedding rows must not retain gradients from the previous window.
		p.grad = 0
	}
}
