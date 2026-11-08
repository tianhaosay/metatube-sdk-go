package javdb

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/metatube-community/metatube-sdk-go/provider/internal/testkit"
)

// TestJavDB_GetMovieInfoByID checks lookup by URL slug and by movie number.
func TestJavDB_GetMovieInfoByID(t *testing.T) {
	testkit.Test(t, New, []string{
		// URL slug.
		"RkdrWK",
		// Movie number.
		"FLAV-359",
	})
}

// TestJavDB_SearchMovie checks movie search by number.
func TestJavDB_SearchMovie(t *testing.T) {
	testkit.Test(t, New, []string{
		"SSIS-033",
		"FLAV-359",
	})
}

// TestJavDB_NormalizeMovieKeyword checks keyword normalization.
func TestJavDB_NormalizeMovieKeyword(t *testing.T) {
	db := New()
	for keyword, want := range map[string]string{
		"ssis-033": "SSIS-033",
		"MIDV005":  "MIDV005",
		"FLAV-359": "FLAV-359",
	} {
		require.Equal(t, want, db.NormalizeMovieKeyword(keyword))
	}
}

// TestJavDB_GetActorInfoByID checks actor lookup by slug.
func TestJavDB_GetActorInfoByID(t *testing.T) {
	testkit.Test(t, New, []string{
		"kp5J",
	})
}
