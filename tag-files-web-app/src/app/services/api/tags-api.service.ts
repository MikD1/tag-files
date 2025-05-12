import {inject, Injectable} from '@angular/core';
import {HttpClient} from '@angular/common/http';
import {Observable} from 'rxjs';

export interface TagStatistics {
  tagName: string;
  usageCount: number;
}

@Injectable({
  providedIn: 'root'
})
export class TagsApiService {
  private baseUrl = 'http://localhost:5001/api';
  private readonly http = inject(HttpClient);

  getTags(): Observable<string[]> {
    return this.http.get<string[]>(`${this.baseUrl}/tags`);
  }

  createTag(tag: string): Observable<void> {
    return this.http.post<void>(`${this.baseUrl}/tags`, tag);
  }

  getTagStatistics(): Observable<TagStatistics[]> {
    return this.http.get<TagStatistics[]>(`${this.baseUrl}/tags/statistics`);
  }
}
