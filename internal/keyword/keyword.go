// Package keyword provides Chinese/English tokenization (gse) and
// corpus-level TF-IDF keyword extraction.
package keyword

import (
	"math"
	"sort"
	"strings"
	"unicode"

	"github.com/go-ego/gse"
)

type Extractor struct {
	seg  gse.Segmenter
	stop map[string]struct{}
}

func New() (*Extractor, error) {
	e := &Extractor{stop: stopwords()}
	// load the embedded default (simplified Chinese) dictionary
	if err := e.seg.LoadDict(); err != nil {
		return nil, err
	}
	return e, nil
}

// Tokens segments text and filters out stopwords, punctuation and
// bare numbers. ASCII tokens are lowercased.
func (e *Extractor) Tokens(text string) []string {
	words := e.seg.Cut(text, true)
	out := make([]string, 0, len(words))
	for _, w := range words {
		w = strings.ToLower(strings.TrimSpace(w))
		if !keep(w) {
			continue
		}
		if _, bad := e.stop[w]; bad {
			continue
		}
		out = append(out, w)
	}
	return out
}

func keep(w string) bool {
	runes := []rune(w)
	if len(runes) < 2 {
		return false
	}
	hasLetter := false
	for _, r := range runes {
		if unicode.IsLetter(r) {
			hasLetter = true
			break
		}
	}
	return hasLetter
}

type Keyword struct {
	Term   string  `json:"term"`
	Weight float64 `json:"weight"`
}

// TFIDF computes top-N keywords for every document against the whole
// corpus. docs[i] is the token list of document i.
func TFIDF(docs [][]string, topN int) [][]Keyword {
	n := float64(len(docs))
	df := map[string]float64{}
	for _, toks := range docs {
		seen := map[string]struct{}{}
		for _, t := range toks {
			if _, ok := seen[t]; !ok {
				seen[t] = struct{}{}
				df[t]++
			}
		}
	}
	out := make([][]Keyword, len(docs))
	for i, toks := range docs {
		if len(toks) == 0 {
			continue
		}
		tf := map[string]float64{}
		for _, t := range toks {
			tf[t]++
		}
		kws := make([]Keyword, 0, len(tf))
		for t, f := range tf {
			w := (f / float64(len(toks))) * math.Log((n+1)/(df[t]+1))
			kws = append(kws, Keyword{Term: t, Weight: w})
		}
		sort.Slice(kws, func(a, b int) bool { return kws[a].Weight > kws[b].Weight })
		if len(kws) > topN {
			kws = kws[:topN]
		}
		out[i] = kws
	}
	return out
}

func stopwords() map[string]struct{} {
	words := strings.Fields(zhStop + " " + enStop)
	m := make(map[string]struct{}, len(words))
	for _, w := range words {
		m[w] = struct{}{}
	}
	return m
}

const zhStop = `的 了 和 是 在 我 有 就 不 人 都 一个 上 也 很 到 说 要 去 你 会 着 没有 看 好 自己
这 那 与 及 或 等 中 为 对 从 被 把 让 向 于 之 其 而 并 但 却 则 即 如 若 因 由
如果 因为 所以 但是 然后 而且 或者 以及 例如 比如 其中 这个 那个 这些 那些 这里 那里
我们 你们 他们 她们 它们 大家 什么 怎么 为什么 如何 哪些 哪个 时候 地方
可以 需要 进行 通过 根据 按照 关于 对于 由于 作为 就是 也是 还是 只是 都是 不是
一些 一种 一下 已经 正在 将会 能够 应该 必须 可能 或许 不会 不能 没 每 各 另 该 此 本 至 以 再 又 更 最 非常 比较 相关 其他 其它 以下 如下 以上 上述`

const enStop = `the a an and or of to in for on with is are was were be been being this that these those
it its as at by from we you they i he she him her them us our your their his my me
not no nor can could should would will shall may might must have has had do does did
if then else when where which who whom whose what how why all any both each few more
most other some such only own same so than too very just about above below between into
through during before after over under again further once here there out off up down s t don etc via e.g i.e`
