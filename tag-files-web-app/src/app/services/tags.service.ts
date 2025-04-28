import { inject, Injectable } from '@angular/core';
import { HttpClient } from '@angular/common/http';
import { Observable } from 'rxjs';

@Injectable({
  providedIn: 'root'
})
export class TagsService {
  getTags(): Observable<string[]> {
    return this.http.get<string[]>(`${this.baseUrl}/tags`);
  }

  createTag(tag: string): Observable<void> {
    return this.http.post<void>(`${this.baseUrl}/tags`, tag);
  }

  private baseUrl = 'http://localhost:5001/api';
  private readonly http = inject(HttpClient);
}
