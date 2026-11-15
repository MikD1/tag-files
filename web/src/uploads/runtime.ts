import { useEffect, useSyncExternalStore } from 'react';
import { invalidateLibrary } from '../api/queries';
import { UploadCoordinator } from './coordinator';
import { uploadAPI } from './api';
import { hashInWorker } from './worker-client';
import { sendPartXHR } from './transport';
let coordinator: UploadCoordinator | undefined;
export function getUploadCoordinator(): UploadCoordinator {
  return coordinator ??= new UploadCoordinator({
    api: uploadAPI,
    storage: { getItem: (key) => localStorage.getItem(key), setItem: (key, value) => localStorage.setItem(key, value) },
    hash: hashInWorker,
    sendPart: sendPartXHR,
    onLibraryChange: () => { void invalidateLibrary(); },
  });
}
export function useUploads(instance: UploadCoordinator = getUploadCoordinator()) {
  const snapshot = useSyncExternalStore(instance.subscribe, instance.getSnapshot, instance.getSnapshot);
  useEffect(() => { void instance.start(); }, [instance]);
  return snapshot;
}
