using MediatR;
using Microsoft.EntityFrameworkCore;
using TagFilesService.Infrastructure;
using TagFilesService.Library.Contracts;
using TagFilesService.Model;

namespace TagFilesService.Library.Handlers;

public class AssignTagsHandler(AppDbContext dbContext) : IRequestHandler<AssignTagsRequest, LibraryItemDto>
{
    public async Task<LibraryItemDto> Handle(AssignTagsRequest request, CancellationToken cancellationToken)
    {
        FileMetadata metadata = await GetMetadataByIdOrThrow(request.FileId);
        List<Tag> tags = await GetTagsByNameOrThrow(request.Tags);

        metadata.Tags.Clear();
        metadata.Tags.AddRange(tags);

        await dbContext.SaveChangesAsync(cancellationToken);
        return LibraryItemDto.FromMetadata(metadata);
    }

    private async Task<FileMetadata> GetMetadataByIdOrThrow(uint id)
    {
        FileMetadata? metadata = await dbContext.FilesMetadata
            .Include(x => x.Tags)
            .FirstOrDefaultAsync(x => x.Id == id);
        if (metadata is null)
        {
            throw new ApplicationException($"Metadata {id} not found");
        }

        return metadata;
    }

    private async Task<List<Tag>> GetTagsByNameOrThrow(List<string> tagNames)
    {
        List<Tag> tags = await dbContext.Tags
            .Where(x => tagNames.Contains(x.Name))
            .ToListAsync();
        if (tags.Count != tagNames.Count)
        {
            throw new ApplicationException("Some tags not found");
        }

        return tags;
    }
}