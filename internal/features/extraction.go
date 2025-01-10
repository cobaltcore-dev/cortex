package features

import (
	"log"

	"github.com/go-pg/pg/v10"
)

var schemaCreators = []func(db *pg.DB) error{
	noisyProjectsSchema,
}

var featureExtractors = []func(db *pg.DB) error{
	noisyProjectsExtractor,
}

func Init(db *pg.DB) {
	for _, schemaCreator := range schemaCreators {
		if err := schemaCreator(db); err != nil {
			log.Fatal(err)
		}
	}
}

func Extract(db *pg.DB) {
	for _, featureExtractor := range featureExtractors {
		if err := featureExtractor(db); err != nil {
			log.Printf("Failed to extract features: %v\n", err)
		}
	}
}
