import { HttpClient } from "@angular/common/http";
import { inject, Injectable } from "@angular/core";
import { map } from "rxjs";

import type { Observable } from "rxjs";

export enum FileType {
  Unknown = "Unknown",
  Text = "Text",
  Image = "Image",
  Video = "Video",
  Audio = "Audio",
}

export interface SearchRequest {
  tagQuery?: string;
  itemType?: FileType;
  pageIndex: number;
  pageSize: number;
}

export interface AssignTagsRequest {
  itemsList: number[];
  tags: string[];
}

export interface LibraryItem {
  id: number;
  path: string;
  thumbnailPath?: string;
  description?: string;
  uploadedOn: string;
  type: FileType;
  mediaType?: string;
  videoDuration?: string;
  tags: string[];
}

export interface LibraryItemPaginatedList {
  items: LibraryItem[];
  totalItems: number;
  pageIndex: number;
  totalPages: number;
}

@Injectable({
  providedIn: "root",
})
export class LibraryApiService {
  private readonly baseUrl = "http://localhost:5001/api";
  private readonly http = inject(HttpClient);

  getItem(id: number): Observable<LibraryItem> {
    return this.http.get<LibraryItem>(`${this.baseUrl}/library/${id}`);
  }

  search(request: SearchRequest): Observable<LibraryItemPaginatedList> {
    return this.http
      .post<LibraryItemPaginatedList>(`${this.baseUrl}/library/search`, request)
      .pipe(
        map((response) => ({
          ...response,
          items: response.items.map((item) => ({
            ...item,
            videoDuration: item.videoDuration
              ? convertDuration(item.videoDuration)
              : undefined,
          })),
        }))
      );
  }

  assignTags(request: AssignTagsRequest): Observable<LibraryItem[]> {
    return this.http.post<LibraryItem[]>(
      `${this.baseUrl}/library/assign-tags`,
      request
    );
  }
}

function convertDuration(input: string): string {
  const [hoursStr, minutesStr, secondsFractionStr] = input.split(":");

  const hours = parseInt(hoursStr, 10);
  const minutes = parseInt(minutesStr, 10);
  const seconds = Math.floor(parseFloat(secondsFractionStr));

  if (hours > 0) {
    return `${hours}:${minutes.toString().padStart(2, "0")}:${seconds.toString().padStart(2, "0")}`;
  } else {
    return `${minutes}:${seconds.toString().padStart(2, "0")}`;
  }
}
