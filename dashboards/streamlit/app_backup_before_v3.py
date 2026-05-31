import os
from datetime import datetime

import pandas as pd
import plotly.express as px
import requests
import streamlit as st


API_BASE_URL = os.getenv("API_BASE_URL", "http://api:8080")

REGION_COLORS = {
    "asia": "#7E57C2",
    "europe": "#43A047",
    "us-east": "#1E88E5",
    "us-west": "#FB8C00",
}

STATUS_COLORS = {
    "healthy": "#2E7D32",
    "degraded": "#F9A825",
    "down": "#C62828",
}

EVENT_COLORS = [
    "#1565C0", "#2E7D32", "#EF6C00", "#6A1B9A",
    "#00897B", "#C62828", "#5D4037", "#3949AB",
    "#AD1457", "#7CB342", "#F9A825", "#00838F",
    "#8E24AA", "#455A64", "#D84315", "#00ACC1",
]


st.set_page_config(
    page_title="Global Service Resiliency Dashboard",
    page_icon="🌐",
    layout="wide",
)

st.markdown(
    """
    <style>
    .block-container {
        padding-top: 2rem;
        padding-bottom: 2rem;
    }
    h1, h2, h3 {
        color: #1f2937;
    }
    [data-testid="stMetricValue"] {
        font-size: 2rem;
        color: #111827;
    }
    .route-card {
        background: #eef6ff;
        border: 1px solid #bfdbfe;
        border-radius: 14px;
        padding: 18px;
        margin-bottom: 16px;
    }
    .top-card {
        background: #f8fafc;
        border: 1px solid #e5e7eb;
        border-radius: 14px;
        padding: 18px;
        margin-bottom: 16px;
    }
    .quality-card {
        background: #fff7ed;
        border: 1px solid #fed7aa;
        border-radius: 14px;
        padding: 14px;
        margin-bottom: 10px;
    }
    .note-card {
        background: #f0f9ff;
        border: 1px solid #bae6fd;
        border-radius: 14px;
        padding: 14px;
        margin-bottom: 14px;
    }
    </style>
    """,
    unsafe_allow_html=True,
)


def fetch_json(path: str):
    url = f"{API_BASE_URL}{path}"
    try:
        response = requests.get(url, timeout=10)
        response.raise_for_status()
        return response.json()
    except Exception as exc:
        st.error(f"Could not load {path} from {API_BASE_URL}: {exc}")
        return None


def as_dataframe(data):
    if not data:
        return pd.DataFrame()
    if isinstance(data, list):
        return pd.DataFrame(data)
    return pd.DataFrame([data])


def status_badge(status: str) -> str:
    status = (status or "unknown").lower()
    if status == "healthy":
        return "🟢 Healthy"
    if status == "degraded":
        return "🟠 Degraded"
    if status == "down":
        return "🔴 Down"
    return f"⚪ {status.title()}"


def format_dt(value):
    if not value:
        return ""
    try:
        return datetime.fromisoformat(str(value).replace("Z", "+00:00")).strftime("%Y-%m-%d %H:%M:%S")
    except Exception:
        return str(value)


st.title("🌐 Global Service Resiliency Dashboard")
st.caption("Phase 3 implementation dashboard consuming the REST API Gateway serving layer")

health = fetch_json("/health")
summary = fetch_json("/api/summary")
events = fetch_json("/api/events/types")
weather = fetch_json("/api/weather/regions")
region_health = fetch_json("/api/regions/health")
routing = fetch_json("/api/routing/current")

if health and health.get("status") == "healthy":
    st.success("API status: healthy")
else:
    st.warning("API status could not be confirmed")

if summary is None:
    st.stop()

events_df = as_dataframe(events)
weather_df = as_dataframe(weather)
health_df = as_dataframe(region_health)

st.header("Executive Summary")

kpi1, kpi2, kpi3, kpi4, kpi5 = st.columns(5)
kpi1.metric("GitHub Events", f"{summary.get('total_github_events', 0):,}")
kpi2.metric("Event Types", summary.get("event_type_count", 0))
kpi3.metric("Weather Regions", summary.get("weather_region_count", 0))
kpi4.metric("Healthy Regions", summary.get("healthy_region_count", 0))
kpi5.metric("Avg Regional Temp", f"{summary.get('avg_regional_temp_c', 0):.1f}°C")

left, right = st.columns([1, 1])

with left:
    st.subheader("Active-Active Routing")
    if routing:
        st.markdown(
            f"""
            <div class="route-card">
                <h3>Preferred Region: {routing.get("preferred_region", "N/A")}</h3>
                <p><b>Status:</b> {status_badge(routing.get("preferred_status"))}</p>
                <p><b>Latency:</b> {routing.get("preferred_latency_ms", "N/A")} ms</p>
                <h4>Fallback Region: {routing.get("fallback_region", "N/A")}</h4>
                <p><b>Fallback Status:</b> {status_badge(routing.get("fallback_status"))}</p>
                <p><b>Reason:</b> {routing.get("reason", "")}</p>
            </div>
            """,
            unsafe_allow_html=True,
        )

with right:
    st.subheader("Top Event Type")
    top_event = summary.get("top_event_type") or {}
    st.markdown(
        f"""
        <div class="top-card">
            <h3>{top_event.get("event_type", "N/A")}</h3>
            <h2>{top_event.get("count", 0):,}</h2>
            <p>Share of events: {top_event.get("percentage", 0):.2f}%</p>
            <p>Window: {format_dt(top_event.get("window_start"))} to {format_dt(top_event.get("window_end"))}</p>
        </div>
        """,
        unsafe_allow_html=True,
    )

st.markdown("---")

st.header("RPT-01: GitHub Event Type Distribution")

if not events_df.empty:
    events_plot_df = events_df.sort_values("count", ascending=True)

    fig_events = px.bar(
        events_plot_df,
        x="count",
        y="event_type",
        orientation="h",
        color="event_type",
        color_discrete_sequence=EVENT_COLORS,
        text=events_plot_df["percentage"].map(lambda x: f"{x:.2f}%"),
        title="GitHub Events by Type",
    )
    fig_events.update_traces(textposition="outside")
    fig_events.update_layout(
        template="plotly_white",
        showlegend=False,
        xaxis_title="Event Count",
        yaxis_title="Event Type",
        height=650,
        margin=dict(l=40, r=40, t=60, b=40),
    )
    st.plotly_chart(fig_events, use_container_width=True)

    display_cols = [
        col for col in [
            "event_type",
            "count",
            "percentage",
            "window_start",
            "window_end",
            "computed_at",
            "region",
        ] if col in events_df.columns
    ]
    st.dataframe(events_df[display_cols].sort_values("count", ascending=False), use_container_width=True)
else:
    st.warning("No event type data available")

st.markdown("---")

st.header("RPT-02: Developer Activity Window")

if not events_df.empty and {"window_start", "window_end", "event_type", "count"}.issubset(events_df.columns):
    st.markdown(
        """
        <div class="note-card">
            This report uses the event aggregation window returned by the serving layer as a temporal activity signal.
            A future API endpoint can extend this into a true hour-by-day heatmap from the processed GitHub records.
        </div>
        """,
        unsafe_allow_html=True,
    )

    activity_df = events_df.copy()
    activity_df["window_start_dt"] = pd.to_datetime(activity_df["window_start"], errors="coerce")
    activity_df["window_end_dt"] = pd.to_datetime(activity_df["window_end"], errors="coerce")
    activity_df = activity_df.dropna(subset=["window_start_dt", "window_end_dt"])
    activity_df = activity_df.sort_values("count", ascending=True)

    if not activity_df.empty:
        fig_activity = px.timeline(
            activity_df,
            x_start="window_start_dt",
            x_end="window_end_dt",
            y="event_type",
            color="event_type",
            color_discrete_sequence=EVENT_COLORS,
            hover_data=["count", "percentage"],
            title="Developer Activity Window by Event Type",
        )
        fig_activity.update_layout(
            template="plotly_white",
            showlegend=False,
            xaxis_title="Activity Window",
            yaxis_title="Event Type",
            height=560,
            margin=dict(l=40, r=40, t=60, b=40),
        )
        st.plotly_chart(fig_activity, use_container_width=True)
    else:
        st.info("Activity window fields are not available in the event type response.")
else:
    st.info("RPT-02 requires window_start and window_end fields from /api/events/types.")

st.markdown("---")

st.header("RPT-03: Regional Weather Dashboard")

if not weather_df.empty:
    c1, c2, c3 = st.columns(3)

    with c1:
        fig_temp = px.bar(
            weather_df,
            x="region",
            y="avg_temp_c",
            color="region",
            color_discrete_map=REGION_COLORS,
            text_auto=".1f",
            title="Average Temperature by Region",
        )
        fig_temp.update_layout(
            template="plotly_white",
            showlegend=False,
            xaxis_title="Region",
            yaxis_title="°C",
            height=420,
        )
        st.plotly_chart(fig_temp, use_container_width=True)

    with c2:
        fig_humidity = px.bar(
            weather_df,
            x="region",
            y="avg_humidity",
            color="region",
            color_discrete_map=REGION_COLORS,
            text_auto=".1f",
            title="Average Humidity by Region",
        )
        fig_humidity.update_layout(
            template="plotly_white",
            showlegend=False,
            xaxis_title="Region",
            yaxis_title="%",
            height=420,
        )
        st.plotly_chart(fig_humidity, use_container_width=True)

    with c3:
        fig_wind = px.bar(
            weather_df,
            x="region",
            y="avg_wind_speed",
            color="region",
            color_discrete_map=REGION_COLORS,
            text_auto=".2f",
            title="Average Wind Speed by Region",
        )
        fig_wind.update_layout(
            template="plotly_white",
            showlegend=False,
            xaxis_title="Region",
            yaxis_title="m/s",
            height=420,
        )
        st.plotly_chart(fig_wind, use_container_width=True)

    st.dataframe(weather_df, use_container_width=True)
else:
    st.warning("No regional weather data available")

st.markdown("---")

st.header("RPT-04: Active-Active Region Health Monitor")

if not health_df.empty:
    health_df["status_display"] = health_df["status"].apply(status_badge)

    c1, c2 = st.columns(2)

    with c1:
        fig_latency = px.bar(
            health_df,
            x="region",
            y="latency_ms",
            color="region",
            color_discrete_map=REGION_COLORS,
            text_auto=True,
            title="Region Latency by Region",
            hover_data=["status", "replication_lag_ms"],
        )
        fig_latency.update_layout(
            template="plotly_white",
            showlegend=False,
            xaxis_title="Region",
            yaxis_title="Latency (ms)",
            height=450,
        )
        st.plotly_chart(fig_latency, use_container_width=True)

    with c2:
        fig_lag = px.bar(
            health_df,
            x="region",
            y="replication_lag_ms",
            color="region",
            color_discrete_map=REGION_COLORS,
            text_auto=True,
            title="Estimated Replication Lag by Region",
            hover_data=["status", "latency_ms"],
        )
        fig_lag.update_layout(
            template="plotly_white",
            showlegend=False,
            xaxis_title="Region",
            yaxis_title="Lag (ms)",
            height=450,
        )
        st.plotly_chart(fig_lag, use_container_width=True)

    status_summary = health_df.groupby("status", dropna=False).size().reset_index(name="region_count")
    fig_status = px.pie(
        status_summary,
        names="status",
        values="region_count",
        color="status",
        color_discrete_map=STATUS_COLORS,
        title="Health Status Mix",
        hole=0.45,
    )
    fig_status.update_layout(template="plotly_white", height=380)
    st.plotly_chart(fig_status, use_container_width=True)

    display_cols = [
        col for col in [
            "region",
            "status_display",
            "latency_ms",
            "replication_lag_ms",
            "last_check",
            "read_preference",
            "write_concern",
        ] if col in health_df.columns
    ]
    st.dataframe(health_df[display_cols], use_container_width=True)
else:
    st.warning("No region health data available")

st.markdown("---")

st.header("RPT-05: Routing Decision and Continuity Signal")

if routing:
    r1, r2, r3, r4 = st.columns(4)
    r1.metric("Preferred Region", routing.get("preferred_region", "N/A"))
    r2.metric("Preferred Latency", f"{routing.get('preferred_latency_ms', 0)} ms")
    r3.metric("Fallback Region", routing.get("fallback_region", "N/A"))
    r4.metric("Fallback Latency", f"{routing.get('fallback_latency_ms', 0)} ms")

    routing_bar_df = pd.DataFrame(
        [
            {
                "role": "Preferred",
                "region": routing.get("preferred_region", "N/A"),
                "latency_ms": routing.get("preferred_latency_ms", 0),
                "status": routing.get("preferred_status", "unknown"),
            },
            {
                "role": "Fallback",
                "region": routing.get("fallback_region", "N/A"),
                "latency_ms": routing.get("fallback_latency_ms", 0),
                "status": routing.get("fallback_status", "unknown"),
            },
        ]
    )

    fig_route = px.bar(
        routing_bar_df,
        x="role",
        y="latency_ms",
        color="region",
        color_discrete_map=REGION_COLORS,
        text="latency_ms",
        hover_data=["region", "status"],
        title="Preferred vs Fallback Route Latency",
    )
    fig_route.update_layout(
        template="plotly_white",
        showlegend=True,
        xaxis_title="Routing Role",
        yaxis_title="Latency (ms)",
        height=420,
    )
    st.plotly_chart(fig_route, use_container_width=True)

    st.markdown(
        f"""
        <div class="route-card">
            <p><b>Decision ID:</b> {routing.get("decision_id", "N/A")}</p>
            <p><b>Reason:</b> {routing.get("reason", "")}</p>
            <p><b>Healthy:</b> {routing.get("healthy_count", 0)} |
               <b>Degraded:</b> {routing.get("degraded_count", 0)} |
               <b>Down:</b> {routing.get("down_count", 0)} |
               <b>Stale:</b> {routing.get("stale_count", 0)}</p>
            <p><b>Decided At:</b> {format_dt(routing.get("decided_at"))}</p>
        </div>
        """,
        unsafe_allow_html=True,
    )
else:
    st.warning("No routing decision available")

st.markdown("---")

st.header("Data Quality and Pipeline Reminders")

reminders = summary.get("data_quality_reminders", [])
if reminders:
    for reminder in reminders:
        st.markdown(
            f"""
            <div class="quality-card">
                {reminder}
            </div>
            """,
            unsafe_allow_html=True,
        )
else:
    st.info("No data quality reminders returned by the API")

st.caption(f"API Source: {API_BASE_URL}")
