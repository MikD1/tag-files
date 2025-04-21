import { AsyncPipe } from '@angular/common';
import { Component, computed, inject } from '@angular/core';
import { MatGridListModule } from '@angular/material/grid-list';
import { ImageCardComponent } from "../image-card/image-card.component";
import { AppStateService } from '../../services/app-state.service';
import { LibraryItemPaginatedList, LibraryService } from '../../services/library.service';
import { Observable } from 'rxjs';

@Component({
  selector: 'app-image-grid',
  imports: [MatGridListModule, ImageCardComponent, AsyncPipe],
  templateUrl: './image-grid.component.html',
  styleUrl: './image-grid.component.scss'
})
export class ImageGridComponent {
  constructor() {
    this.libraryItemsList = this.libraryService.search({ tagQuery: "", pageIndex: 1, pageSize: 100 })
  }

  libraryItemsList: Observable<LibraryItemPaginatedList>;

  protected readonly getGridColumns = computed(() => {
    return this.appStateService.getMainGridColumns();
  })

  private readonly appStateService = inject(AppStateService);
  private readonly libraryService = inject(LibraryService);
}
