interface Props {
  hasMore: boolean;
  onNext: () => void;
  onReset: () => void;
  loading: boolean;
}

export default function Pagination({ hasMore, onNext, onReset, loading }: Props) {
  return (
    <div className="pagination">
      <button onClick={onReset} disabled={loading}>First Page</button>
      <button onClick={onNext} disabled={!hasMore || loading}>
        {loading ? 'Loading...' : 'Next Page'}
      </button>
    </div>
  );
}
