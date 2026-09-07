package main

import "math"

// value is one scalar in the computation graph. Backward runs serially even
// when independent attention heads build their graphs in parallel.
type value struct {
	data, grad float64
	parents    []*value
	propagate  func()
}

func scalar(x float64) *value { return &value{data: x} }
func add(a, b *value) *value {
	out := &value{data: a.data + b.data, parents: []*value{a, b}}
	out.propagate = func() { a.grad += out.grad; b.grad += out.grad }
	return out
}
func mul(a, b *value) *value {
	out := &value{data: a.data * b.data, parents: []*value{a, b}}
	out.propagate = func() { a.grad += b.data * out.grad; b.grad += a.data * out.grad }
	return out
}
func scale(a *value, factor float64) *value {
	out := &value{data: a.data * factor, parents: []*value{a}}
	out.propagate = func() { a.grad += factor * out.grad }
	return out
}
func power(a *value, exponent float64) *value {
	out := &value{data: math.Pow(a.data, exponent), parents: []*value{a}}
	out.propagate = func() { a.grad += exponent * math.Pow(a.data, exponent-1) * out.grad }
	return out
}
func relu(a *value) *value {
	out := &value{data: math.Max(0, a.data), parents: []*value{a}}
	out.propagate = func() {
		if a.data > 0 {
			a.grad += out.grad
		}
	}
	return out
}

// softmax subtracts the maximum to avoid overflowing exp. Its vector-Jacobian
// product includes cross terms: changing one logit changes every probability.
func softmax(logits []*value) []*value {
	probabilities := make([]float64, len(logits))
	maximum := logits[0].data
	for _, x := range logits {
		maximum = math.Max(maximum, x.data)
	}
	total := 0.0
	for i, x := range logits {
		probabilities[i] = math.Exp(x.data - maximum)
		total += probabilities[i]
	}
	result := make([]*value, len(logits))
	for i := range logits {
		probabilities[i] /= total
	}
	for i := range logits {
		out := &value{data: probabilities[i], parents: logits}
		out.propagate = func() {
			for j, x := range logits {
				derivative := -probabilities[i] * probabilities[j]
				if i == j {
					derivative += probabilities[i]
				}
				x.grad += derivative * out.grad
			}
		}
		result[i] = out
	}
	return result
}

// Fused log-sum-exp cross entropy is stable even for extreme logits.
func crossEntropy(logits []*value, target int) *value {
	maximum := logits[0].data
	for _, x := range logits {
		maximum = math.Max(maximum, x.data)
	}
	probabilities := make([]float64, len(logits))
	sum := 0.0
	for i, x := range logits {
		probabilities[i] = math.Exp(x.data - maximum)
		sum += probabilities[i]
	}
	out := &value{data: maximum + math.Log(sum) - logits[target].data, parents: logits}
	out.propagate = func() {
		for i, x := range logits {
			g := probabilities[i] / sum
			if i == target {
				g--
			}
			x.grad += g * out.grad
		}
	}
	return out
}

func backward(loss *value) {
	visited := make(map[*value]bool)
	order := make([]*value, 0)
	var visit func(*value)
	visit = func(v *value) {
		if visited[v] {
			return
		}
		visited[v] = true
		for _, parent := range v.parents {
			visit(parent)
		}
		v.grad = 0
		order = append(order, v)
	}
	visit(loss)
	loss.grad = 1
	for i := len(order) - 1; i >= 0; i-- {
		if order[i].propagate != nil {
			order[i].propagate()
		}
	}
}
