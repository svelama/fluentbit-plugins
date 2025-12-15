FROM fluent/fluent-bit:2.1

LABEL maintainer="your-email@example.com"
LABEL version="basic"
LABEL description="Fluent Bit K8s to Splunk plugin - Basic version (~500 logs/sec)"

# Copy plugin binary
COPY out_k8s_splunk.so /fluent-bit/plugins/

# Set permissions
RUN chmod 755 /fluent-bit/plugins/out_k8s_splunk.so

# Set log level
ENV FLB_LOG_LEVEL=info

# Verify plugin exists
RUN ls -lh /fluent-bit/plugins/out_k8s_splunk.so
