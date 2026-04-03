import { useParams, Link } from 'react-router-dom';
import { useFetch } from '../../hooks/useApi';
import { api } from '../../api/client';
import StatusBadge from '../../components/StatusBadge';
import TimeAgo from '../../components/TimeAgo';
import type { Job } from '../../types/api';

const CANCELLABLE = ['queued', 'dispatched', 'cdm_hold', 'pending'];
const RETRIABLE = ['failed', 'timed_out'];

export default function JobDetail() {
  const { jobId } = useParams<{ jobId: string }>();
  const { data: job, loading, error, refetch } = useFetch<Job>(`/jobs/${jobId}`);

  if (loading) return <p>Loading...</p>;
  if (error) return <p className="error">{error}</p>;
  if (!job) return <p>Job not found.</p>;

  async function cancel() {
    await api.post(`/jobs/${jobId}/cancel`);
    refetch();
  }

  async function retry() {
    const res = await api.post<{ job_id: string }>(`/jobs/${jobId}/retry`);
    window.location.href = `/jobs/${res.job_id}`;
  }

  return (
    <div>
      <div className="page-header">
        <h2>Job {job.id.slice(0, 12)}...</h2>
        <div className="actions">
          {CANCELLABLE.includes(job.status) && <button className="btn btn-danger" onClick={cancel}>Cancel</button>}
          {RETRIABLE.includes(job.status) && <button className="btn btn-primary" onClick={retry}>Retry</button>}
        </div>
      </div>

      <dl>
        <dt>Status</dt><dd><StatusBadge status={job.status} /></dd>
        <dt>Type</dt><dd>{job.type}</dd>
        <dt>Device</dt><dd><Link to={`/devices/${job.device_id}`}>{job.device_id}</Link></dd>
        {job.parent_job_id && <><dt>Parent Job</dt><dd><Link to={`/jobs/${job.parent_job_id}`}>{job.parent_job_id}</Link></dd></>}
        <dt>Retries</dt><dd>{job.retry_count} / {job.max_retries}</dd>
        {job.last_error && <><dt>Last Error</dt><dd className="error">{job.last_error}</dd></>}
        <dt>Created</dt><dd>{new Date(job.created_at).toLocaleString()}</dd>
        <dt>Dispatched</dt><dd><TimeAgo ts={job.dispatched_at} /></dd>
        <dt>Acknowledged</dt><dd><TimeAgo ts={job.acknowledged_at} /></dd>
        <dt>Started</dt><dd><TimeAgo ts={job.started_at} /></dd>
        <dt>Completed</dt><dd><TimeAgo ts={job.completed_at} /></dd>
      </dl>

      <h3>Payload</h3>
      <pre className="code-block">{JSON.stringify(job.payload, null, 2)}</pre>

      {job.result && (
        <>
          <h3>Result</h3>
          <dl>
            <dt>Exit Code</dt><dd>{job.result.exit_code}</dd>
          </dl>
          {job.result.stdout && (
            <>
              <h4>stdout</h4>
              <pre className="code-block">{job.result.stdout}</pre>
            </>
          )}
          {job.result.stderr && (
            <>
              <h4>stderr</h4>
              <pre className="code-block error">{job.result.stderr}</pre>
            </>
          )}
        </>
      )}
    </div>
  );
}
