// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package plugins

// Index of all supported feature extractors, keyed by name.
// Each extractor registers itself here through a func init() in its own file.
var Index = map[string]func() FeatureExtractor{}
