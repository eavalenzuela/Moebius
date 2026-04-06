import { useState, useEffect, useCallback, useTransition } from 'react';
import { api } from '../api/client';

interface FetchState<T> {
  data: T | null;
  loading: boolean;
  error: string | null;
}

export function useFetch<T>(path: string | null) {
  const [state, setState] = useState<FetchState<T>>({
    data: null,
    loading: !!path,
    error: null,
  });
  const [fetchKey, setFetchKey] = useState(0);
  const [, startTransition] = useTransition();

  useEffect(() => {
    if (!path) return;
    let cancelled = false;
    startTransition(() => {
      setState((prev) => ({ ...prev, loading: true, error: null }));
    });
    api.get<T>(path)
      .then((result) => {
        if (!cancelled) setState({ data: result, loading: false, error: null });
      })
      .catch((e: Error) => {
        if (!cancelled) setState((prev) => ({ ...prev, loading: false, error: e.message }));
      });
    return () => { cancelled = true; };
  }, [path, fetchKey]);

  const refetch = useCallback(() => {
    setFetchKey((k) => k + 1);
  }, []);

  return { ...state, refetch };
}
