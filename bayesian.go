package bayesian

import (
	"encoding/gob"
	"encoding/json"
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"sync/atomic"
)

// defaultProb is the tiny non-zero probability that a word
// we have not seen before appears in the class.
const defaultProb = 0.00000000001

// ErrUnderflow is returned when an underflow is detected.
var ErrUnderflow = errors.New("possible underflow detected")

// Class defines a class that the classifier will filter:
// C = {C_1, ..., C_n}. You should define your classes as a
// set of constants, for example as follows:
//
//    const (
//        Good Class = "Good"
//        Bad Class = "Bad
//    )
//
// Class values should be unique.
type Class string

// Classifier implements the Naive Bayesian Classifier.
type Classifier struct {
	Classes         []Class
	learned         int   // docs learned
	seen            int64 // docs seen
	datas           map[Class]*classData
	tfIdf           bool
	DidConvertTfIdf bool // we can't classify a TF-IDF classifier if we haven't yet
	// called ConverTermsFreqToTfIdf
}

// serializableClassifier represents a container for
// Classifier objects whose fields are modifiable by
// reflection and are therefore writeable by gob.
type serializableClassifier struct {
	Classes         []Class              `json:"classes"`
	Learned         int                  `json:"learned"`
	Seen            int                  `json:"seen"`
	Datas           map[Class]*classData `json:"datas"`
	TfIdf           bool                 `json:"tf_idf"`
	DidConvertTfIdf bool                 `json:"did_convert_tf_idf"`
}

// classData holds the frequency data for words in a
// particular class. In the future, we may replace this
// structure with a trie-like structure for more
// efficient storage.
type classData struct {
	Freqs   map[string]float64   `json:"freqs"`
	FreqTfs map[string][]float64 `json:"freqTfs"`
	Total   int                  `json:"total"`
}

// newClassData creates a new empty classData node.
func newClassData() *classData {
	return &classData{
		Freqs:   make(map[string]float64),
		FreqTfs: make(map[string][]float64),
	}
}

// getWordProb returns P(W|C_j) -- the probability of seeing
// a particular word W in a document of this class.
func (d *classData) getWordProb(word string) float64 {
	value, ok := d.Freqs[word]
	if !ok {
		return defaultProb
	}
	return value / float64(d.Total)
}

// getWordsProb returns P(D|C_j) -- the probability of seeing
// this set of words in a document of this class.
//
// Note that words should not be empty, and this method of
// calulation is prone to underflow if there are many words
// and their individual probabilties are small.
func (d *classData) getWordsProb(words []string) (prob float64) {
	prob = 1
	for _, word := range words {
		prob *= d.getWordProb(word)
	}
	return
}

// NewClassifierTfIdf returns a new classifier. The classes provided
// should be at least 2 in number and unique, or this method will
// panic.
func NewClassifierTfIdf(classes ...Class) (c *Classifier) {
	n := len(classes)

	// check size
	if n < 2 {
		panic("provide at least two classes")
	}

	// check uniqueness
	check := make(map[Class]bool, n)
	for _, class := range classes {
		check[class] = true
	}
	if len(check) != n {
		panic("classes must be unique")
	}
	// create the classifier
	c = &Classifier{
		Classes: classes,
		datas:   make(map[Class]*classData, n),
		tfIdf:   true,
	}
	for _, class := range classes {
		c.datas[class] = newClassData()
	}
	return
}

// NewClassifier returns a new classifier. The classes provided
// should be at least 2 in number and unique, or this method will
// panic.
func NewClassifier(classes ...Class) (c *Classifier) {
	n := len(classes)

	// check size
	if n < 2 {
		panic("provide at least two classes")
	}

	// check uniqueness
	check := make(map[Class]bool, n)
	for _, class := range classes {
		check[class] = true
	}
	if len(check) != n {
		panic("classes must be unique")
	}
	// create the classifier
	c = &Classifier{
		Classes:         classes,
		datas:           make(map[Class]*classData, n),
		tfIdf:           false,
		DidConvertTfIdf: false,
	}
	for _, class := range classes {
		c.datas[class] = newClassData()
	}
	return
}

// NewClassifierFromFile loads an existing classifier from
// file. The classifier was previously saved with a call
// to c.WriteToFile(string).
func NewClassifierFromFile(name string) (c *Classifier, err error) {
	file, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	return NewClassifierFromReader(file)
}

// NewClassifierFromReader This actually does the deserializing of a Gob encoded classifier
func NewClassifierFromReader(r io.Reader) (c *Classifier, err error) {
	dec := gob.NewDecoder(r)
	w := new(serializableClassifier)
	err = dec.Decode(w)

	return &Classifier{w.Classes, w.Learned, int64(w.Seen), w.Datas, w.TfIdf, w.DidConvertTfIdf}, err
}

// NewClassifierFromJson This actually does the deserializing of a Gob encoded classifier
func NewClassifierFromJson(data []byte) (c *Classifier, err error) {
	w := new(serializableClassifier)

	err = json.Unmarshal(data, w)

	if err != nil {
		return nil, err
	}

	return &Classifier{
		w.Classes,
		w.Learned,
		int64(w.Seen),
		w.Datas,
		w.TfIdf,
		w.DidConvertTfIdf,
	}, err
}

// getPriors returns the prior probabilities for the
// classes provided -- P(C_j).
//
// TODO: There is a way to smooth priors, currently
// not implemented here.
func (c *Classifier) getPriors() (priors []float64) {
	n := len(c.Classes)
	priors = make([]float64, n, n)
	sum := 0
	for index, class := range c.Classes {
		total := c.datas[class].Total
		priors[index] = float64(total)
		sum += total
	}
	if sum != 0 {
		for i := 0; i < n; i++ {
			priors[i] /= float64(sum)
		}
	}
	return
}

// Learned returns the number of documents ever learned
// in the lifetime of this classifier.
func (c *Classifier) Learned() int {
	return c.learned
}

// Seen returns the number of documents ever classified
// in the lifetime of this classifier.
func (c *Classifier) Seen() int {
	return int(atomic.LoadInt64(&c.seen))
}

// IsTfIdf returns true if we are a classifier of type TfIdf
func (c *Classifier) IsTfIdf() bool {
	return c.tfIdf
}

// WordCount returns the number of words counted for
// each class in the lifetime of the classifier.
func (c *Classifier) WordCount() (result []int) {
	result = make([]int, len(c.Classes))
	for inx, class := range c.Classes {
		data := c.datas[class]
		result[inx] = data.Total
	}
	return
}

// Observe should be used when word-frequencies have been already been learned
// externally (e.g., hadoop)
func (c *Classifier) Observe(word string, count int, which Class) {
	data := c.datas[which]
	data.Freqs[word] += float64(count)
	data.Total += count
}

// Learn will accept new training documents for
// supervised learning.
func (c *Classifier) Learn(document []string, which Class) {

	// If we are a tfidf classifier we first need to get terms as
	// terms frequency and store that to work out the idf part later
	// in ConvertToIDF().
	if c.tfIdf {
		if c.DidConvertTfIdf {
			panic("Cannot call ConvertTermsFreqToTfIdf more than once. Reset and relearn to reconvert.")
		}

		// Term Frequency: word count in document / document length
		docTf := make(map[string]float64)
		for _, word := range document {
			docTf[word]++
		}

		docLen := float64(len(document))

		for wIndex, wCount := range docTf {
			docTf[wIndex] = wCount / docLen
			// add the TF sample, after training we can get IDF values.
			c.datas[which].FreqTfs[wIndex] = append(c.datas[which].FreqTfs[wIndex], docTf[wIndex])
		}

	}

	data := c.datas[which]
	for _, word := range document {
		data.Freqs[word]++
		data.Total++
	}
	c.learned++
}

// ConvertTermsFreqToTfIdf uses all the TF samples for the class and converts
// them to TF-IDF https://en.wikipedia.org/wiki/Tf%E2%80%93idf
// once we have finished learning all the classes and have the totals.
func (c *Classifier) ConvertTermsFreqToTfIdf() {

	if c.DidConvertTfIdf {
		panic("Cannot call ConvertTermsFreqToTfIdf more than once. Reset and relearn to reconvert.")
	}

	for className := range c.datas {

		for wIndex := range c.datas[className].FreqTfs {
			tfIdfAdder := float64(0)

			for tfSampleIndex := range c.datas[className].FreqTfs[wIndex] {

				// we always want a possitive TF-IDF score.
				tf := c.datas[className].FreqTfs[wIndex][tfSampleIndex]
				c.datas[className].FreqTfs[wIndex][tfSampleIndex] = math.Log1p(tf) * math.Log1p(float64(c.learned)/float64(c.datas[className].Total))
				tfIdfAdder += c.datas[className].FreqTfs[wIndex][tfSampleIndex]
			}
			// convert the 'counts' to TF-IDF's
			c.datas[className].Freqs[wIndex] = tfIdfAdder
		}

	}

	// sanity check
	c.DidConvertTfIdf = true

}

// LogScores produces "log-likelihood"-like scores that can
// be used to classify documents into classes.
//
// The value of the score is proportional to the likelihood,
// as determined by the classifier, that the given document
// belongs to the given class. This is true even when scores
// returned are negative, which they will be (since we are
// taking logs of probabilities).
//
// The index j of the score corresponds to the class given
// by c.Classes[j].
//
// Additionally returned are "inx" and "strict" values. The
// inx corresponds to the maximum score in the array. If more
// than one of the scores holds the maximum values, then
// strict is false.
//
// Unlike c.Probabilities(), this function is not prone to
// floating point underflow and is relatively safe to use.
func (c *Classifier) LogScores(document []string) (scores []float64, inx int, strict bool) {
	if c.tfIdf && !c.DidConvertTfIdf {
		panic("Using a TF-IDF classifier. Please call ConvertTermsFreqToTfIdf before calling LogScores.")
	}

	n := len(c.Classes)
	scores = make([]float64, n, n)
	priors := c.getPriors()

	// calculate the score for each class
	for index, class := range c.Classes {
		data := c.datas[class]
		// c is the sum of the logarithms
		// as outlined in the refresher
		score := math.Log(priors[index])
		for _, word := range document {
			score += math.Log(data.getWordProb(word))
		}
		scores[index] = score
	}
	inx, strict = findMax(scores)
	atomic.AddInt64(&c.seen, 1)
	return scores, inx, strict
}

// ProbScores works the same as LogScores, but delivers
// actual probabilities as discussed above. Note that float64
// underflow is possible if the word list contains too
// many words that have probabilities very close to 0.
//
// Notes on underflow: underflow is going to occur when you're
// trying to assess large numbers of words that you have
// never seen before. Depending on the application, this
// may or may not be a concern. Consider using SafeProbScores()
// instead.
func (c *Classifier) ProbScores(doc []string) (scores []float64, inx int, strict bool) {
	if c.tfIdf && !c.DidConvertTfIdf {
		panic("Using a TF-IDF classifier. Please call ConvertTermsFreqToTfIdf before calling ProbScores.")
	}
	n := len(c.Classes)
	scores = make([]float64, n, n)
	priors := c.getPriors()
	sum := float64(0)
	// calculate the score for each class
	for index, class := range c.Classes {
		data := c.datas[class]
		// c is the sum of the logarithms
		// as outlined in the refresher
		score := priors[index]
		for _, word := range doc {
			score *= data.getWordProb(word)
		}
		scores[index] = score
		sum += score
	}
	for i := 0; i < n; i++ {
		scores[i] /= sum
	}
	inx, strict = findMax(scores)
	atomic.AddInt64(&c.seen, 1)
	return scores, inx, strict
}

// SafeProbScores works the same as ProbScores, but is
// able to detect underflow in those cases where underflow
// results in the reverse classification. If an underflow is detected,
// this method returns an ErrUnderflow, allowing the user to deal with it as
// necessary. Note that underflow, under certain rare circumstances,
// may still result in incorrect probabilities being returned,
// but this method guarantees that all error-less invokations
// are properly classified.
//
// Underflow detection is more costly because it also
// has to make additional log score calculations.
func (c *Classifier) SafeProbScores(doc []string) (scores []float64, inx int, strict bool, err error) {
	if c.tfIdf && !c.DidConvertTfIdf {
		panic("Using a TF-IDF classifier. Please call ConvertTermsFreqToTfIdf before calling SafeProbScores.")
	}

	n := len(c.Classes)
	scores = make([]float64, n, n)
	logScores := make([]float64, n, n)
	priors := c.getPriors()
	sum := float64(0)
	// calculate the score for each class
	for index, class := range c.Classes {
		data := c.datas[class]
		// c is the sum of the logarithms
		// as outlined in the refresher
		score := priors[index]
		logScore := math.Log(priors[index])
		for _, word := range doc {
			p := data.getWordProb(word)
			score *= p
			logScore += math.Log(p)
		}
		scores[index] = score
		logScores[index] = logScore
		sum += score
	}
	for i := 0; i < n; i++ {
		scores[i] /= sum
	}
	inx, strict = findMax(scores)
	logInx, logStrict := findMax(logScores)

	// detect underflow -- the size
	// relation between scores and logScores
	// must be preserved or something is wrong
	if inx != logInx || strict != logStrict {
		err = ErrUnderflow
	}
	atomic.AddInt64(&c.seen, 1)
	return scores, inx, strict, err
}

// WordFrequencies returns a matrix of word frequencies that currently
// exist in the classifier for each class state for the given input
// words. In other words, if you obtain the frequencies
//
//    freqs := c.WordFrequencies(/* [j]string */)
//
// then the expression freq[i][j] represents the frequency of the j-th
// word within the i-th class.
func (c *Classifier) WordFrequencies(words []string) (freqMatrix [][]float64) {
	n, l := len(c.Classes), len(words)
	freqMatrix = make([][]float64, n)
	for i := range freqMatrix {
		arr := make([]float64, l)
		data := c.datas[c.Classes[i]]
		for j := range arr {
			arr[j] = data.getWordProb(words[j])
		}
		freqMatrix[i] = arr
	}
	return
}

// WordsByClass returns a map of words and their probability of
// appearing in the given class.
func (c *Classifier) WordsByClass(class Class) (freqMap map[string]float64) {
	freqMap = make(map[string]float64)
	for word, cnt := range c.datas[class].Freqs {
		freqMap[word] = cnt / float64(c.datas[class].Total)
	}

	return freqMap
}

// WriteToFile serializes this classifier to a file.
func (c *Classifier) WriteToFile(name string) (err error) {
	file, err := os.OpenFile(name, os.O_WRONLY|os.O_CREATE, 0644)
	if err != nil {
		return err
	}
	defer file.Close()

	_, err = c.WriteTo(file)

	return err
}

// WriteClassesToFile writes all classes to file.
func (c *Classifier) WriteClassesToFile(rootPath string) (err error) {
	for name := range c.datas {
		err = c.WriteClassToFile(name, rootPath)
	}

	return
}

// WriteClassToFile writes a single class to file.
func (c *Classifier) WriteClassToFile(name Class, rootPath string) (err error) {
	data := c.datas[name]
	fileName := filepath.Join(rootPath, string(name))
	file, err := os.OpenFile(fileName, os.O_WRONLY|os.O_CREATE, 0644)
	if err != nil {
		return err
	}
	defer file.Close()

	enc := gob.NewEncoder(file)
	err = enc.Encode(data)
	return
}

// WriteTo serializes this classifier to GOB and write to Writer.
func (c *Classifier) WriteTo(w io.Writer) (n int64, err error) {
	enc := gob.NewEncoder(w)
	err = enc.Encode(&serializableClassifier{c.Classes, c.learned, int(c.seen), c.datas, c.tfIdf, c.DidConvertTfIdf})

	return
}

// ReadClassFromFile loads existing class data from a
// file.
func (c *Classifier) ReadClassFromFile(class Class, location string) (err error) {
	fileName := filepath.Join(location, string(class))
	file, err := os.Open(fileName)

	if err != nil {
		return err
	}
	defer file.Close()

	dec := gob.NewDecoder(file)
	w := new(classData)
	err = dec.Decode(w)

	c.learned++
	c.datas[class] = w
	return
}

func (c *Classifier) ToJson() ([]byte, error) {
	data := &serializableClassifier{
		c.Classes,
		c.learned,
		int(c.seen),
		c.datas,
		c.tfIdf,
		c.DidConvertTfIdf,
	}

	result, err := json.Marshal(data)

	if err != nil {
		return nil, err
	}

	return result, nil
}

// findMax finds the maximum of a set of scores; if the
// maximum is strict -- that is, it is the single unique
// maximum from the set -- then strict has return value
// true. Otherwise it is false.
func findMax(scores []float64) (inx int, strict bool) {
	inx = 0
	strict = true
	for i := 1; i < len(scores); i++ {
		if scores[inx] < scores[i] {
			inx = i
			strict = true
		} else if scores[inx] == scores[i] {
			strict = false
		}
	}
	return
}
