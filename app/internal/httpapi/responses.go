package httpapi

import "net/http"

// A persisted command replays the original status/headers/JSON bytes, including
// explicit failure results. These visitors implement the generated strict
// response interfaces; payloads are constructed with the generated DTOs.
func (r response) VisitListCollectionsResponse(w http.ResponseWriter) error       { return r.write(w) }
func (r response) VisitCreateCollectionResponse(w http.ResponseWriter) error      { return r.write(w) }
func (r response) VisitDeleteCollectionResponse(w http.ResponseWriter) error      { return r.write(w) }
func (r response) VisitGetCollectionResponse(w http.ResponseWriter) error         { return r.write(w) }
func (r response) VisitRenameCollectionResponse(w http.ResponseWriter) error      { return r.write(w) }
func (r response) VisitListItemsResponse(w http.ResponseWriter) error             { return r.write(w) }
func (r response) VisitReplaceBulkCollectionResponse(w http.ResponseWriter) error { return r.write(w) }
func (r response) VisitReplaceBulkMetadataResponse(w http.ResponseWriter) error   { return r.write(w) }
func (r response) VisitReplaceBulkTagsResponse(w http.ResponseWriter) error       { return r.write(w) }
func (r response) VisitGetItemResponse(w http.ResponseWriter) error               { return r.write(w) }
func (r response) VisitPatchItemResponse(w http.ResponseWriter) error             { return r.write(w) }
func (r response) VisitSetFavoriteResponse(w http.ResponseWriter) error           { return r.write(w) }
func (r response) VisitGetSimilarResponse(w http.ResponseWriter) error            { return r.write(w) }
func (r response) VisitRecordViewResponse(w http.ResponseWriter) error            { return r.write(w) }
func (r response) VisitCreateNavigationContextResponse(w http.ResponseWriter) error {
	return r.write(w)
}
func (r response) VisitGetNeighborsResponse(w http.ResponseWriter) error      { return r.write(w) }
func (r response) VisitGetOperationResponse(w http.ResponseWriter) error      { return r.write(w) }
func (r response) VisitListSavedSearchesResponse(w http.ResponseWriter) error { return r.write(w) }
func (r response) VisitCreateSavedSearchResponse(w http.ResponseWriter) error { return r.write(w) }
func (r response) VisitDeleteSavedSearchResponse(w http.ResponseWriter) error { return r.write(w) }
func (r response) VisitGetSavedSearchResponse(w http.ResponseWriter) error    { return r.write(w) }
func (r response) VisitPatchSavedSearchResponse(w http.ResponseWriter) error  { return r.write(w) }
func (r response) VisitGetTagStatisticsResponse(w http.ResponseWriter) error  { return r.write(w) }
func (r response) VisitListTagsResponse(w http.ResponseWriter) error          { return r.write(w) }
func (r response) VisitCreateTagResponse(w http.ResponseWriter) error         { return r.write(w) }
func (r response) VisitRenameTagResponse(w http.ResponseWriter) error         { return r.write(w) }
