package javdb

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/metatube-community/metatube-sdk-go/provider/internal/testkit"
)

func TestJavDB_GetMovieInfoByID(t *testing.T) {
	testkit.Test(t, New, []string{
		// Slug IDs.
		"RkdrWK",
		// Numbers.
		"FLAV-359",
	})
}

func TestJavDB_SearchMovie(t *testing.T) {
	testkit.Test(t, New, []string{
		"SSIS-033",
		"FLAV-359",
	})
}

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

func TestJavDB_GetActorInfoByID(t *testing.T) {
	testkit.Test(t, New, []string{
		"kp5J", // 美丘里美 / 美丘さとみ
	})
}
