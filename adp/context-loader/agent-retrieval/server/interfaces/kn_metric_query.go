// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package interfaces

// Metric query is the third step of the OT-first metric path: an object type has
// been recalled, a metric of that object type has been chosen from
// get_object_types' related_metrics, and now it is computed — by its own
// definition, in ontology-query, rather than re-derived in SQL.
//
// The request mirrors ontology-query's MetricQueryRequestBody
// (POST .../metrics/{metric_id}/data) so nothing is lost in translation.

// QueryMetricReq is the query_metric request.
type QueryMetricReq struct {
	KnID     string `json:"kn_id" form:"kn_id"`
	MetricID string `json:"metric_id" form:"metric_id"`

	Time               *MetricTimeWindow `json:"time,omitempty"`
	Cond               *KnCondition      `json:"condition,omitempty"`
	AnalysisDimensions []string          `json:"analysis_dimensions,omitempty"`
	OrderBy            []*MetricOrderBy  `json:"order_by,omitempty"`
	Having             *MetricHaving     `json:"having,omitempty"`
	Limit              *int              `json:"limit,omitempty"`
	// FillNull pads the steps of a range query that produced no data point.
	// Sent as a URL query parameter, exactly as ontology-query expects it.
	FillNull bool `json:"fill_null,omitempty"`
}

// MetricTimeWindow is the time block of a metric query.
//
// Instant=true asks for one point (a "how much is it now" question) and takes no
// step; Instant=false asks for a series and requires one. Start/End are unix
// timestamps in seconds.
type MetricTimeWindow struct {
	Start   *int64  `json:"start,omitempty"`
	End     *int64  `json:"end,omitempty"`
	Instant *bool   `json:"instant,omitempty"`
	Step    *string `json:"step,omitempty"`
}

// MetricOrderBy is one ordering entry of a metric query.
type MetricOrderBy struct {
	Property  string `json:"property"`
	Direction string `json:"direction"`
}

// MetricHaving filters the aggregated result of a metric query.
type MetricHaving struct {
	Field     string `json:"field"`
	Operation string `json:"operation"`
	Value     any    `json:"value,omitempty"`
}

// QueryMetricResp is the query_metric response.
//
// It carries the computed series and nothing else: ontology-query also echoes the
// metric definition back under "model", which is the same formula the caller just
// read from related_metrics, and repeating it in every data response is exactly
// the kind of payload this tool surface trims elsewhere.
type QueryMetricResp struct {
	KnID      string              `json:"kn_id"`
	MetricID  string              `json:"metric_id"`
	Datas     []*MetricDataSeries `json:"datas"`
	OverallMs int64               `json:"overall_ms,omitempty"`
}

// MetricDataSeries is one labelled series of a metric result, matching
// ontology-query's BknMetricData.
type MetricDataSeries struct {
	Labels       map[string]string `json:"labels,omitempty"`
	Times        []any             `json:"times,omitempty"`
	TimeStrs     []string          `json:"time_strs,omitempty"`
	Values       []any             `json:"values"`
	GrowthValues []any             `json:"growth_values,omitempty"`
	GrowthRates  []any             `json:"growth_rates,omitempty"`
	Proportions  []any             `json:"proportions,omitempty"`
}

// MetricQueryDownstreamReq is what goes on the wire to ontology-query: the request
// without the routing fields that live in the URL.
type MetricQueryDownstreamReq struct {
	Time               *MetricTimeWindow `json:"time,omitempty"`
	Cond               *KnCondition      `json:"condition,omitempty"`
	AnalysisDimensions []string          `json:"analysis_dimensions,omitempty"`
	OrderBy            []*MetricOrderBy  `json:"order_by,omitempty"`
	Having             *MetricHaving     `json:"having,omitempty"`
	Limit              *int              `json:"limit,omitempty"`
}

// MetricQueryDownstreamResp is ontology-query's MetricResponse, of which only the
// series are forwarded.
type MetricQueryDownstreamResp struct {
	Datas     []*MetricDataSeries `json:"datas"`
	OverallMs int64               `json:"overall_ms,omitempty"`
}
