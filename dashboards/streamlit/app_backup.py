"""Streamlit dashboard for Global Service Resiliency Phase 3.

The dashboard consumes the REST API Gateway rather than connecting directly to
MongoDB. This keeps visualization decoupled from the storage layer and mirrors
how a production dashboard would consume a serving API.
"""

from __future__ import annotations

import os
from datetime import datetime, timezone
from typing import Any

import pandas as pd
import plotly.express as px
import requests
import streamlit as st


DEFAULT_API_BASE_URL = os.getenv("API_BASE_URL", "http://localhost:8080")
REQUEST_TIMEOUT_SECONDS = int(os.getenv("DASHBOARD_REQUEST_TIMEOUT_SECONDS", "10"))


st.set_page_config(
    page_title="Global Service Resiliency Dashboard",
    page_icon="🌐",
    layout="wide",
)


@st.cache_data(ttl=30, show_spinner=False)
def fetch_json(api_base_url: str, endpoint: str) -> Any:
    """Fetch JSON from the API Gateway."""
    url = f"{api_base_url.rstrip('/')}{endpoint}"
    response = requests.get(url, timeout=REQUEST_TIMEOUT_SECONDS)
    response.raise_for_status()
    return response.json()


def safe_fetch(api_base_url: str, endpoint: str, fallback: Any) -> tuple[Any, str | None]:
    """Return API data and an optional error message without breaking the page."""
    try:
        return fetch_json(api_base_url, endpoint), None
    except Exception as exc:  # noqa: BLE001 - display full dashboard-friendly error
        return fallback, f"{endpoint}: {exc}"


def to_df(records: Any) -> pd.DataFrame:
    if isinstance(records, list):
        return pd.DataFrame(records)
    if isinstance(records, dict):
        return pd.DataFrame([records])
    return pd.DataFrame()


def fmt_number(value: Any) -> str:
    try:
        numeric = float(value)
    except (TypeError, ValueError):
        return "0"
    if numeric.is_integer():
        return f"{int(numeric):,}"
    return f"{numeric:,.2f}"


def status_badge(status: str) -> str:
    status = (status or "unknown").lower()
    if status == "healthy":
        return "🟢 Healthy"
    if status == "degraded":
        return "🟠 Degraded"
    if status == "down":
        return "🔴 Down"
    return f"⚪ {status.title()}"


st.title("🌐 Global Service Resiliency Dashboard")
st.caption("Phase 3 implementation dashboard consuming the REST API Gateway serving layer")

with st.sidebar:
    st.header("Connection")
    api_base_url = st.text_input("API Gateway base URL", value=DEFAULT_API_BASE_URL)
    st.caption("Local manual run: http://localhost:8080")
    st.caption("Docker Compose dashboard: http://api:8080")
    if st.button("Refresh dashboard", use_container_width=True):
        st.cache_data.clear()
        st.rerun()
    st.divider()
    st.caption(f"Last rendered: {datetime.now(timezone.utc).strftime('%Y-%m-%d %H:%M:%S UTC')}")

health, health_err = safe_fetch(api_base_url, "/health", {})
summary, summary_err = safe_fetch(api_base_url, "/api/summary", {})
events, events_err = safe_fetch(api_base_url, "/api/events/types", [])
weather, weather_err = safe_fetch(api_base_url, "/api/weather/regions", [])
region_health, region_health_err = safe_fetch(api_base_url, "/api/regions/health", [])
routing, routing_err = safe_fetch(api_base_url, "/api/routing/current", {})

errors = [err for err in [health_err, summary_err, events_err, weather_err, region_health_err, routing_err] if err]
if errors:
    st.error("Some API calls failed. Confirm the API service is running and reachable.")
    with st.expander("API errors"):
        for err in errors:
            st.code(err)
else:
    st.success(f"API status: {health.get('status', 'unknown')}")

# ── Executive KPIs ─────────────────────────────────────────────────────────────
st.subheader("Executive Summary")

kpi1, kpi2, kpi3, kpi4, kpi5 = st.columns(5)
kpi1.metric("GitHub Events", fmt_number(summary.get("total_github_events", 0)))
kpi2.metric("Event Types", fmt_number(summary.get("event_type_count", 0)))
kpi3.metric("Weather Regions", fmt_number(summary.get("weather_region_count", 0)))
kpi4.metric("Healthy Regions", fmt_number(summary.get("healthy_region_count", 0)))
kpi5.metric("Avg Regional Temp", f"{float(summary.get('avg_regional_temp_c', 0) or 0):.1f}°C")

route_col, top_col = st.columns([1, 1])
with route_col:
    st.markdown("### Active-Active Routing")
    preferred_region = routing.get("preferred_region") or summary.get("current_routing", {}).get("preferred_region", "")
    fallback_region = routing.get("fallback_region") or summary.get("current_routing", {}).get("fallback_region", "")
    reason = routing.get("reason") or summary.get("current_routing", {}).get("reason", "No routing decision available")
    st.metric("Preferred Region", preferred_region or "None")
    st.metric("Fallback Region", fallback_region or "None")
    st.caption(reason)

with top_col:
    st.markdown("### Top Event Type")
    top_event = summary.get("top_event_type") or {}
    st.metric(top_event.get("event_type", "No data"), fmt_number(top_event.get("count", 0)))
    st.caption(f"Share of events: {top_event.get('percentage', 0)}%")
    if top_event.get("window_start") and top_event.get("window_end"):
        st.caption(f"Window: {top_event['window_start']} to {top_event['window_end']}")

st.divider()

# ── RPT-01: Event Type Distribution ───────────────────────────────────────────
st.subheader("RPT-01: GitHub Event Type Distribution")
events_df = to_df(events)
if not events_df.empty:
    events_df = events_df.sort_values("count", ascending=False)
    fig = px.bar(
        events_df,
        x="count",
        y="event_type",
        orientation="h",
        text="percentage",
        title="GitHub Events by Type",
        labels={"count": "Event Count", "event_type": "Event Type", "percentage": "Percentage"},
    )
    fig.update_layout(yaxis={"categoryorder": "total ascending"}, height=520)
    fig.update_traces(texttemplate="%{text:.2f}%", textposition="outside")
    st.plotly_chart(fig, use_container_width=True)
    with st.expander("Event type data"):
        st.dataframe(events_df, use_container_width=True, hide_index=True)
else:
    st.warning("No event type data returned from the API.")

# ── RPT-03: Regional Weather Dashboard ────────────────────────────────────────
st.subheader("RPT-03: Regional Weather Dashboard")
weather_df = to_df(weather)
if not weather_df.empty:
    c1, c2, c3 = st.columns(3)
    with c1:
        fig = px.bar(weather_df, x="region", y="avg_temp_c", title="Average Temperature by Region", labels={"avg_temp_c": "°C"})
        st.plotly_chart(fig, use_container_width=True)
    with c2:
        fig = px.bar(weather_df, x="region", y="avg_humidity", title="Average Humidity by Region", labels={"avg_humidity": "%"})
        st.plotly_chart(fig, use_container_width=True)
    with c3:
        fig = px.bar(weather_df, x="region", y="avg_wind_speed", title="Average Wind Speed by Region", labels={"avg_wind_speed": "m/s"})
        st.plotly_chart(fig, use_container_width=True)
    with st.expander("Regional weather data"):
        st.dataframe(weather_df, use_container_width=True, hide_index=True)
else:
    st.warning("No regional weather data returned from the API.")

# ── RPT-04: Region Health Monitor ─────────────────────────────────────────────
st.subheader("RPT-04: Active-Active Region Health Monitor")
health_df = to_df(region_health)
if not health_df.empty:
    health_df["status_display"] = health_df["status"].apply(status_badge)
    h1, h2 = st.columns([1, 1])
    with h1:
        fig = px.bar(health_df, x="region", y="latency_ms", color="status", title="Region Latency by Health Status", labels={"latency_ms": "Latency (ms)"})
        st.plotly_chart(fig, use_container_width=True)
    with h2:
        fig = px.bar(health_df, x="region", y="replication_lag_ms", color="status", title="Estimated Replication Lag", labels={"replication_lag_ms": "Lag (ms)"})
        st.plotly_chart(fig, use_container_width=True)
    st.dataframe(
        health_df[["region", "status_display", "latency_ms", "replication_lag_ms", "last_check", "read_preference", "write_concern"]],
        use_container_width=True,
        hide_index=True,
    )
else:
    st.warning("No region health data returned from the API.")

# ── RPT-05: Routing / Replication Lag Analysis ────────────────────────────────
st.subheader("RPT-05: Routing Decision and Continuity Signal")
if routing:
    r1, r2, r3, r4 = st.columns(4)
    r1.metric("Preferred", routing.get("preferred_region", "None"), routing.get("preferred_status", ""))
    r2.metric("Preferred Latency", f"{routing.get('preferred_latency_ms', 0)} ms")
    r3.metric("Fallback", routing.get("fallback_region", "None"), routing.get("fallback_status", ""))
    r4.metric("Fallback Latency", f"{routing.get('fallback_latency_ms', 0)} ms")
    st.info(routing.get("reason", "No routing reason returned."))
    with st.expander("Routing decision JSON"):
        st.json(routing)
else:
    st.warning("No routing decision returned from the API.")

# ── Data quality reminders ───────────────────────────────────────────────────
st.subheader("Data Quality Reminders")
reminders = summary.get("data_quality_reminders", []) if isinstance(summary, dict) else []
if reminders:
    for item in reminders:
        st.markdown(f"- {item}")
else:
    st.markdown("- No data quality reminders returned from the API.")

with st.expander("Raw API payload summary"):
    st.json({
        "health": health,
        "summary": summary,
        "routing": routing,
    })
