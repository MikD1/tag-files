import { NgFor } from '@angular/common';
import { Component, computed, inject } from '@angular/core';
import { MatGridListModule } from '@angular/material/grid-list';
import { ImageCardComponent } from "../image-card/image-card.component";
import { FileMetadata } from '../../model/file-metadata';
import { AppStateService } from '../../services/app-state.service';

@Component({
  selector: 'app-image-grid',
  imports: [NgFor, MatGridListModule, ImageCardComponent],
  templateUrl: './image-grid.component.html',
  styleUrl: './image-grid.component.scss'
})
export class ImageGridComponent {
  protected filesMetadata: FileMetadata[] = Array(21).fill({
    url: 'https://images.pexels.com/photos/2014422/pexels-photo-2014422.jpeg'
  });

  protected readonly getGridColumns = computed(() => {
    return this.appStateService.getMainGridColumns();
  })

  private readonly appStateService = inject(AppStateService);
}
