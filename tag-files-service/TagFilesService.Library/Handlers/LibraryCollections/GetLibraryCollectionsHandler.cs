using MediatR;
using Microsoft.EntityFrameworkCore;
using TagFilesService.Infrastructure;
using TagFilesService.Library.Contracts;
using TagFilesService.Library.Contracts.LibraryCollections;
using TagFilesService.Model;

namespace TagFilesService.Library.Handlers.LibraryCollections;

public class GetLibraryCollectionsHandler(AppDbContext dbContext)
    : IRequestHandler<GetLibraryCollectionsRequest, List<LibraryCollectionDto>>
{
    public async Task<List<LibraryCollectionDto>> Handle(GetLibraryCollectionsRequest request,
        CancellationToken cancellationToken)
    {
        List<LibraryCollection> collections = await dbContext.Collections
            .OrderBy(x => x.Name)
            .ToListAsync(cancellationToken);

        List<LibraryCollectionDto> dto = [];
        foreach (LibraryCollection collection in collections)
        {
            LibraryItem? collectionItem = await dbContext.LibraryItems
                .Where(x => x.CollectionId == collection.Id)
                .OrderBy(x => x.UploadedOn)
                .FirstOrDefaultAsync(cancellationToken);

            string? coverPath = null;
            if (collectionItem is not null)
            {
                coverPath = LibraryItemDto.FromModel(collectionItem).ThumbnailPath;
            }

            dto.Add(LibraryCollectionDto.FromModel(collection, coverPath));
        }

        return dto;
    }
}