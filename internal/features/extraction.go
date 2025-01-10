package features

import (
	"log"
)

var schemaCreators = []func() error{
	noisyProjectsSchema,
}

var featureExtractors = []func() error{
	noisyProjectsExtractor,
}

func Init() {
	for _, schemaCreator := range schemaCreators {
		if err := schemaCreator(); err != nil {
			log.Fatal(err)
		}
	}
}

func Extract() {
	for _, featureExtractor := range featureExtractors {
		if err := featureExtractor(); err != nil {
			log.Printf("Failed to extract features: %v\n", err)
		}
	}
}
