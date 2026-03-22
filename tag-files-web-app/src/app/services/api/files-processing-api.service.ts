import {inject, Injectable} from '@angular/core';
import {HttpClient} from '@angular/common/http';
import {Observable} from 'rxjs';

export type ProcessingStatus =
  | 'WaitingForUpload'
  | 'Pending'
  | 'Converting'
  | 'AddedToLibrary'
  | 'TemporaryFileDeleted'
  | 'LibraryItemSaved'
  | 'Done'
  | 'Failed';

export interface ProcessingFileDto {
  id: number;
  originalFileName: string;
  libraryFileName: string;
  status: ProcessingStatus;
}

@Injectable({
  providedIn: 'root'
})
export class FilesProcessingApiService {
  private readonly baseUrl = 'http://localhost:5001/api';
  private readonly http = inject(HttpClient);

  getProcessingFiles(): Observable<ProcessingFileDto[]> {
    return this.http.get<ProcessingFileDto[]>(`${this.baseUrl}/files-processing/files`);
  }
}
